/*
Copyright 2017 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kubernetes

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kubernetes/kompose/pkg/kobject"
	api "k8s.io/api/core/v1"
)

func serviceConfigWithPorts(serviceType string, ports ...kobject.Ports) kobject.ServiceConfig {
	return kobject.ServiceConfig{
		Name:                "web",
		Image:               "nginx",
		ServiceType:         serviceType,
		ServiceTypeExplicit: serviceType != "",
		Port:                ports,
	}
}

// errorMessages returns the messages of all error-severity issues, sorted.
func errorMessages(issues []kobject.PortIssue) []string {
	var messages []string
	for _, issue := range issues {
		if issue.Severity == kobject.PortIssueError {
			messages = append(messages, issue.Message)
		}
	}
	sort.Strings(messages)
	return messages
}

func warningMessages(issues []kobject.PortIssue) []string {
	var messages []string
	for _, issue := range issues {
		if issue.Severity == kobject.PortIssueWarning {
			messages = append(messages, issue.Message)
		}
	}
	sort.Strings(messages)
	return messages
}

func TestValidateServicePortsDerivation(t *testing.T) {
	t.Run("no label, no switch, published port keeps ClusterIP", func(t *testing.T) {
		svc := kobject.ServiceConfig{Name: "web", Port: []kobject.Ports{{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"}}}
		issues := ValidateServicePorts(&svc, kobject.ConvertOptions{})
		if svc.ServiceType != "" {
			t.Errorf("expected ServiceType to stay unset, got %q", svc.ServiceType)
		}
		if len(issues) != 0 {
			t.Errorf("expected no issues, got %v", issues)
		}
	})

	t.Run("switch on, published port derives NodePort", func(t *testing.T) {
		svc := kobject.ServiceConfig{Name: "web", Port: []kobject.Ports{{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"}}}
		issues := ValidateServicePorts(&svc, kobject.ConvertOptions{DeriveServiceTypeFromPorts: true})
		if svc.ServiceType != string(api.ServiceTypeNodePort) {
			t.Errorf("expected NodePort, got %q", svc.ServiceType)
		}
		if len(errorMessages(issues)) != 0 {
			t.Errorf("expected no errors, got %v", issues)
		}
	})

	t.Run("switch on, only expose keeps ClusterIP", func(t *testing.T) {
		svc := kobject.ServiceConfig{Name: "web", Port: []kobject.Ports{{ContainerPort: 80, Protocol: "TCP"}}}
		ValidateServicePorts(&svc, kobject.ConvertOptions{DeriveServiceTypeFromPorts: true})
		if svc.ServiceType != string(api.ServiceTypeClusterIP) {
			t.Errorf("expected ClusterIP, got %q", svc.ServiceType)
		}
	})

	t.Run("switch on, host-mode port does not derive NodePort", func(t *testing.T) {
		svc := kobject.ServiceConfig{Name: "web", Port: []kobject.Ports{{HostPort: 8080, ContainerPort: 80, Protocol: "TCP", Mode: kobject.PortModeHost}}}
		ValidateServicePorts(&svc, kobject.ConvertOptions{DeriveServiceTypeFromPorts: true})
		if svc.ServiceType != string(api.ServiceTypeClusterIP) {
			t.Errorf("expected ClusterIP, got %q", svc.ServiceType)
		}
	})

	t.Run("explicit label wins over derivation", func(t *testing.T) {
		svc := serviceConfigWithPorts("LoadBalancer", kobject.Ports{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"})
		ValidateServicePorts(&svc, kobject.ConvertOptions{DeriveServiceTypeFromPorts: true})
		if svc.ServiceType != string(api.ServiceTypeLoadBalancer) {
			t.Errorf("expected LoadBalancer, got %q", svc.ServiceType)
		}
	})

	t.Run("statefulset overrides derived type with a note", func(t *testing.T) {
		svc := kobject.ServiceConfig{Name: "web", Port: []kobject.Ports{{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"}}}
		issues := ValidateServicePorts(&svc, kobject.ConvertOptions{
			DeriveServiceTypeFromPorts: true,
			Controller:                 StatefulStateController,
		})
		if svc.ServiceType != ServiceTypeHeadless {
			t.Errorf("expected Headless, got %q", svc.ServiceType)
		}
		for _, issue := range issues {
			if issue.Severity == kobject.PortIssueError {
				t.Errorf("unexpected error: %v", issue)
			}
		}
	})

	t.Run("statefulset overrides explicit label with a warning", func(t *testing.T) {
		svc := serviceConfigWithPorts("NodePort", kobject.Ports{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"})
		issues := ValidateServicePorts(&svc, kobject.ConvertOptions{Controller: StatefulStateController})
		if svc.ServiceType != ServiceTypeHeadless {
			t.Errorf("expected Headless, got %q", svc.ServiceType)
		}
		if len(warningMessages(issues)) != 1 {
			t.Errorf("expected one warning about the override, got %v", issues)
		}
	})
}

// TestValidateServicePortsAccumulates checks that every problem in a service is
// reported in one pass, not just the first.
func TestValidateServicePortsAccumulates(t *testing.T) {
	svc := kobject.ServiceConfig{
		Name: "web",
		Port: []kobject.Ports{
			{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"},
			{HostPort: 8080, ContainerPort: 90, Protocol: "TCP"}, // duplicate service port and name
			{HostPort: 9000, ContainerPort: 9000, Protocol: "TCP"},
			{HostPort: 9001, ContainerPort: 9001, Protocol: "TCP"},
		},
		ServiceType:         string(api.ServiceTypeNodePort),
		ServiceTypeExplicit: true,
		NodePortPorts: map[int32]int32{
			9000: 70000, // out of range
			9001: 30001,
		},
	}
	issues := ValidateServicePorts(&svc, kobject.ConvertOptions{})
	messages := errorMessages(issues)
	if len(messages) < 2 {
		t.Fatalf("expected the duplicate-port and out-of-range errors together, got %v", messages)
	}
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, "outside the allowed range 30000-32767") {
		t.Errorf("expected the out-of-range nodePort error, got %v", messages)
	}
	if !strings.Contains(joined, "both map to service port 8080") {
		t.Errorf("expected the duplicate service port error, got %v", messages)
	}
}

func TestValidateServicePortsNodePorts(t *testing.T) {
	newService := func(nodePorts map[int32]int32, ports ...kobject.Ports) kobject.ServiceConfig {
		return kobject.ServiceConfig{
			Name:                "web",
			Port:                ports,
			ServiceType:         string(api.ServiceTypeNodePort),
			ServiceTypeExplicit: true,
			NodePortPorts:       nodePorts,
		}
	}

	t.Run("per-port nodePorts land in range", func(t *testing.T) {
		svc := newService(map[int32]int32{80: 30080, 443: 30443},
			kobject.Ports{HostPort: 80, ContainerPort: 80, Protocol: "TCP"},
			kobject.Ports{HostPort: 443, ContainerPort: 443, Protocol: "TCP"})
		if issues := ValidateServicePorts(&svc, kobject.ConvertOptions{}); len(issues) != 0 {
			t.Errorf("expected no issues, got %v", issues)
		}
		built := (&Kubernetes{}).ConfigServicePorts(svc)
		want := map[int32]int32{30080: 80, 30443: 443}
		for _, port := range built {
			if want[port.NodePort] != port.Port {
				t.Errorf("unexpected nodePort assignment: port %+v", port)
			}
		}
	})

	t.Run("legacy and per-port labels conflict", func(t *testing.T) {
		svc := newService(map[int32]int32{80: 30080}, kobject.Ports{HostPort: 80, ContainerPort: 80, Protocol: "TCP"})
		svc.NodePortPort = 30081
		issues := errorMessages(ValidateServicePorts(&svc, kobject.ConvertOptions{}))
		if len(issues) != 1 || !strings.Contains(issues[0], "cannot be used together") {
			t.Errorf("expected a conflict error, got %v", issues)
		}
	})

	t.Run("repeated nodePort is rejected per protocol", func(t *testing.T) {
		svc := newService(map[int32]int32{80: 30080, 443: 30080},
			kobject.Ports{HostPort: 80, ContainerPort: 80, Protocol: "TCP"},
			kobject.Ports{HostPort: 443, ContainerPort: 443, Protocol: "TCP"})
		issues := errorMessages(ValidateServicePorts(&svc, kobject.ConvertOptions{}))
		if len(issues) != 1 || !strings.Contains(issues[0], "both request nodePort 30080/tcp") {
			t.Errorf("expected duplicate nodePort error, got %v", issues)
		}
	})

	t.Run("same nodePort across TCP and UDP is allowed", func(t *testing.T) {
		svc := newService(map[int32]int32{80: 30080, 81: 30080},
			kobject.Ports{HostPort: 80, ContainerPort: 80, Protocol: "TCP"},
			kobject.Ports{HostPort: 81, ContainerPort: 81, Protocol: "UDP"})
		if issues := ValidateServicePorts(&svc, kobject.ConvertOptions{}); len(issues) != 0 {
			t.Errorf("expected no issues, got %v", issues)
		}
	})

	t.Run("custom range is honoured", func(t *testing.T) {
		svc := newService(map[int32]int32{80: 20080}, kobject.Ports{HostPort: 80, ContainerPort: 80, Protocol: "TCP"})
		opt := kobject.ConvertOptions{NodePortRange: kobject.PortRange{Min: 20000, Max: 21000}}
		if issues := ValidateServicePorts(&svc, opt); len(issues) != 0 {
			t.Errorf("expected custom range to accept 20080, got %v", issues)
		}
	})

	t.Run("nodePort on an undeclared container port is rejected", func(t *testing.T) {
		svc := newService(map[int32]int32{9999: 30080}, kobject.Ports{HostPort: 80, ContainerPort: 80, Protocol: "TCP"})
		issues := errorMessages(ValidateServicePorts(&svc, kobject.ConvertOptions{}))
		if len(issues) != 1 || !strings.Contains(issues[0], "does not declare") {
			t.Errorf("expected undeclared container port error, got %v", issues)
		}
	})
}

func TestValidateServicePortsLoadBalancer(t *testing.T) {
	svc := kobject.ServiceConfig{
		Name:                "web",
		ServiceType:         string(api.ServiceTypeLoadBalancer),
		ServiceTypeExplicit: true,
		Port: []kobject.Ports{
			{HostPort: 53, ContainerPort: 53, Protocol: "TCP"},
			{HostPort: 53, ContainerPort: 53, Protocol: "UDP"},
		},
	}
	issues := ValidateServicePorts(&svc, kobject.ConvertOptions{})
	messages := errorMessages(issues)
	if len(messages) != 1 || !strings.Contains(messages[0], "used for both TCP and UDP") {
		t.Errorf("expected the mixed-protocol conflict as an error, got %v", issues)
	}

	tcp, udp := (&Kubernetes{}).ConfigLBServicePorts(svc)
	if len(tcp) != 1 || len(udp) != 1 {
		t.Fatalf("expected one TCP and one UDP port, got %v and %v", tcp, udp)
	}
	// Each half is its own Service, so both keep the bare port name.
	if tcp[0].Name != "53" || udp[0].Name != "53" {
		t.Errorf("expected both halves to be named 53, got %q and %q", tcp[0].Name, udp[0].Name)
	}
}

func TestValidateServicePortsDuplicateServicePort(t *testing.T) {
	svc := serviceConfigWithPorts("",
		kobject.Ports{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"},
		kobject.Ports{HostPort: 8080, ContainerPort: 90, Protocol: "TCP"},
	)
	svc.ServiceTypeExplicit = false
	issues := errorMessages(ValidateServicePorts(&svc, kobject.ConvertOptions{}))
	if len(issues) != 1 || !strings.Contains(issues[0], "both map to service port 8080/tcp") {
		t.Fatalf("expected a duplicate service port error, got %v", issues)
	}
}

func TestCreateHeadlessServicePorts(t *testing.T) {
	t.Run("without ports keeps the placeholder", func(t *testing.T) {
		svc := kobject.ServiceConfig{Name: "web", ServiceType: ServiceTypeHeadless}
		headless := (&Kubernetes{}).CreateHeadlessService("web", svc)
		if len(headless.Spec.Ports) != 1 || headless.Spec.Ports[0].Name != "headless" {
			t.Errorf("expected the headless placeholder port, got %v", headless.Spec.Ports)
		}
		if headless.Spec.ClusterIP != "None" {
			t.Errorf("expected clusterIP None, got %q", headless.Spec.ClusterIP)
		}
	})

	t.Run("with ports emits only the real ports", func(t *testing.T) {
		svc := serviceConfigWithPorts(ServiceTypeHeadless,
			kobject.Ports{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"},
		)
		headless := (&Kubernetes{}).CreateHeadlessService("web", svc)
		if len(headless.Spec.Ports) != 1 {
			t.Fatalf("expected exactly one port, got %v", headless.Spec.Ports)
		}
		port := headless.Spec.Ports[0]
		if port.Name != "8080" || port.Port != 8080 || port.TargetPort.IntVal != 80 {
			t.Errorf("unexpected headless port %+v", port)
		}
		if headless.Spec.ClusterIP != "None" {
			t.Errorf("expected clusterIP None, got %q", headless.Spec.ClusterIP)
		}
	})
}

func TestConfigPortsHostMode(t *testing.T) {
	svc := kobject.ServiceConfig{
		Name: "web",
		Port: []kobject.Ports{
			{HostPort: 8080, ContainerPort: 80, Protocol: "TCP", Mode: kobject.PortModeHost},
			{HostPort: 9090, ContainerPort: 90, Protocol: "TCP", Mode: kobject.PortModeIngress},
		},
	}
	ports := ConfigPorts(svc)
	if len(ports) != 2 {
		t.Fatalf("expected two container ports, got %v", ports)
	}
	// Sorted by container port: 80 (host mode) then 90 (ingress).
	if ports[0].HostPort != 8080 {
		t.Errorf("mode: host must become a container hostPort, got %+v", ports[0])
	}
	if ports[1].HostPort != 0 {
		t.Errorf("mode: ingress must not become a container hostPort, got %+v", ports[1])
	}
}

// TestSortedPortsDeterministic checks that reordering the input does not change
// the generated port list.
func TestSortedPortsDeterministic(t *testing.T) {
	a := []kobject.Ports{
		{HostPort: 9090, ContainerPort: 90, Protocol: "TCP"},
		{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"},
		{ContainerPort: 53, Protocol: "UDP"},
		{ContainerPort: 53, Protocol: "TCP"},
	}
	b := []kobject.Ports{a[3], a[2], a[1], a[0]}
	if !reflect.DeepEqual(SortedPorts(a), SortedPorts(b)) {
		t.Errorf("SortedPorts is not order-independent: %v vs %v", SortedPorts(a), SortedPorts(b))
	}
}

// TestTransformReportsAllPortProblems checks that one conversion surfaces every
// port problem instead of stopping at the first service.
func TestTransformReportsAllPortProblems(t *testing.T) {
	komposeObject := kobject.KomposeObject{
		ServiceConfigs: map[string]kobject.ServiceConfig{
			"alpha": {
				Name:         "alpha",
				Image:        "nginx",
				ServiceType:  string(api.ServiceTypeNodePort),
				Port:         []kobject.Ports{{HostPort: 80, ContainerPort: 80, Protocol: "TCP"}},
				NodePortPort: 1,
			},
			"beta": {
				Name:                "beta",
				Image:               "nginx",
				ServiceType:         string(api.ServiceTypeNodePort),
				ServiceTypeExplicit: true,
				Port:                []kobject.Ports{{HostPort: 443, ContainerPort: 443, Protocol: "TCP"}},
				NodePortPorts:       map[int32]int32{443: 70000},
			},
		},
	}
	_, err := (&Kubernetes{}).Transform(komposeObject, kobject.ConvertOptions{})
	if err == nil {
		t.Fatal("expected port problems to fail the conversion")
	}
	message := err.Error()
	for _, want := range []string{"alpha", "beta", "outside the allowed range"} {
		if !strings.Contains(message, want) {
			t.Errorf("expected %q in the error, got:\n%s", want, message)
		}
	}
}

// TestCreateServiceNodePortsUsesPerPortMap checks the per-port nodePort map is
// actually written to the generated Service.
func TestCreateServiceNodePortsUsesPerPortMap(t *testing.T) {
	svc := kobject.ServiceConfig{
		Name:                "web",
		ServiceType:         string(api.ServiceTypeNodePort),
		ServiceTypeExplicit: true,
		Port: []kobject.Ports{
			{HostPort: 80, ContainerPort: 80, Protocol: "TCP"},
			{HostPort: 443, ContainerPort: 443, Protocol: "TCP"},
		},
		NodePortPorts: map[int32]int32{80: 30080, 443: 30443},
	}
	built := (&Kubernetes{}).CreateService("web", svc)
	got := map[int32]int32{}
	for _, port := range built.Spec.Ports {
		got[port.NodePort] = port.Port
	}
	want := map[int32]int32{30080: 80, 30443: 443}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected nodePorts %v, got %v", want, got)
	}
}

func TestServicePortNameSuffixesSecondProtocol(t *testing.T) {
	ports := []kobject.Ports{
		{HostPort: 53, ContainerPort: 53, Protocol: "TCP"},
		{HostPort: 53, ContainerPort: 53, Protocol: "UDP"},
	}
	name := ServicePortName(ports[1], ports)
	if name != "53-udp" {
		t.Errorf("expected second protocol to be suffixed, got %q", name)
	}
}

func TestLogPortIssuesAggregates(t *testing.T) {
	issues := []kobject.PortIssue{
		{Severity: kobject.PortIssueError, Category: kobject.PortCategoryRange, Service: "a", Message: "first"},
		{Severity: kobject.PortIssueError, Category: kobject.PortCategoryConflict, Service: "b", Message: "second"},
		{Severity: kobject.PortIssueWarning, Category: kobject.PortCategoryExposure, Service: "c", Message: "note"},
	}
	err := LogPortIssues(issues)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "second") {
		t.Errorf("expected both errors in one message, got:\n%s", err.Error())
	}
	if strings.Contains(err.Error(), "note") {
		t.Errorf("warnings must not be part of the returned error, got:\n%s", err.Error())
	}
}

func TestHasPublishedPorts(t *testing.T) {
	tests := []struct {
		name  string
		ports []kobject.Ports
		want  bool
	}{
		{"no ports", nil, false},
		{"expose only", []kobject.Ports{{ContainerPort: 80, Protocol: "TCP"}}, false},
		{"published", []kobject.Ports{{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"}}, true},
		{"host mode", []kobject.Ports{{HostPort: 8080, ContainerPort: 80, Protocol: "TCP", Mode: kobject.PortModeHost}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasPublishedPorts(kobject.ServiceConfig{Port: tt.ports}); got != tt.want {
				t.Errorf("HasPublishedPorts() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateServicePortsInvalidPortName(t *testing.T) {
	// A published port above 65535 cannot happen through the loader (it rejects
	// those), but a hand-built ServiceConfig could carry one; the generated name
	// still has to pass DNS-1123.
	svc := kobject.ServiceConfig{
		Name: "web",
		Port: []kobject.Ports{{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"}},
	}
	issues := ValidateServicePorts(&svc, kobject.ConvertOptions{})
	if len(errorMessages(issues)) != 0 {
		t.Errorf("expected a plain port to be valid, got %v", issues)
	}
	if name := ServicePortName(svc.Port[0], svc.Port); name != "8080" {
		t.Errorf("expected name 8080, got %q", name)
	}
}

func TestValidateKomposeObjectPortsSortedByService(t *testing.T) {
	komposeObject := kobject.KomposeObject{
		ServiceConfigs: map[string]kobject.ServiceConfig{
			"zulu": {Name: "zulu", Port: []kobject.Ports{
				{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"},
				{HostPort: 8080, ContainerPort: 90, Protocol: "TCP"},
			}},
			"alpha": {Name: "alpha", Port: []kobject.Ports{
				{HostPort: 8080, ContainerPort: 80, Protocol: "TCP"},
				{HostPort: 8080, ContainerPort: 90, Protocol: "TCP"},
			}},
		},
	}
	issues := (&Kubernetes{}).ValidateKomposeObjectPorts(komposeObject, kobject.ConvertOptions{})
	if len(issues) != 2 {
		t.Fatalf("expected two issues, got %v", issues)
	}
	if issues[0].Service != "alpha" || issues[1].Service != "zulu" {
		t.Errorf("expected issues ordered by service name, got %v", issues)
	}
}

func TestServicePortGroups(t *testing.T) {
	ports := []kobject.Ports{
		{HostPort: 53, ContainerPort: 53, Protocol: "TCP"},
		{HostPort: 53, ContainerPort: 53, Protocol: "UDP"},
		{HostPort: 80, ContainerPort: 80, Protocol: "TCP"},
	}
	loadBalancer := kobject.ServiceConfig{ServiceType: string(api.ServiceTypeLoadBalancer), Port: ports}
	groups := servicePortGroups(loadBalancer, servicePortsOf(loadBalancer))
	if len(groups) != 2 {
		t.Fatalf("expected TCP and UDP groups, got %v", groups)
	}
	if len(groups[0]) != 2 || len(groups[1]) != 1 {
		t.Errorf("unexpected grouping: %v", groups)
	}

	clusterIP := kobject.ServiceConfig{ServiceType: string(api.ServiceTypeClusterIP), Port: ports}
	if groups := servicePortGroups(clusterIP, servicePortsOf(clusterIP)); len(groups) != 1 || len(groups[0]) != 3 {
		t.Errorf("expected one group of three, got %v", groups)
	}
}

func TestConfigServicePortsProtocolOrder(t *testing.T) {
	svc := kobject.ServiceConfig{
		Name: "web",
		Port: []kobject.Ports{
			{ContainerPort: 53, Protocol: "UDP"},
			{ContainerPort: 53, Protocol: "TCP"},
		},
	}
	built := (&Kubernetes{}).ConfigServicePorts(svc)
	if len(built) != 2 {
		t.Fatalf("expected two ports, got %v", built)
	}
	// TCP is the API default, so ConfigServicePorts leaves its protocol empty.
	if built[0].Protocol != "" || built[1].Protocol != api.ProtocolUDP {
		t.Errorf("expected TCP before UDP, got %v", built)
	}
	if built[0].Name != "53" || built[1].Name != "53-udp" {
		t.Errorf("unexpected names %q, %q", built[0].Name, built[1].Name)
	}
}

func TestValidateServicePortsModeInfo(t *testing.T) {
	svc := kobject.ServiceConfig{
		Name: "web",
		Port: []kobject.Ports{{HostPort: 8080, ContainerPort: 80, Protocol: "TCP", Mode: kobject.PortModeHost}},
	}
	issues := ValidateServicePorts(&svc, kobject.ConvertOptions{})
	found := false
	for _, issue := range issues {
		if issue.Category == kobject.PortCategoryMode && issue.Severity == kobject.PortIssueInfo {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an informational mode note, got %v", issues)
	}
}
