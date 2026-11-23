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
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kubernetes/kompose/pkg/kobject"
	log "github.com/sirupsen/logrus"
	api "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// serviceTypeHeadless is the kompose-internal Service type for headless
// services. It cannot live in package compose because the transformer must not
// import the loader.
const serviceTypeHeadless = ServiceTypeHeadless

// ServiceTypeHeadless is the kompose-internal Service type for headless
// services. It is exported for the OpenShift transformer, which shares the port
// and Service building helpers.
const ServiceTypeHeadless = "Headless"

// SortedPorts returns the ports ordered by container port, protocol, host port,
// host IP and mode. Compose preserves declaration order, but generated output
// must not depend on it: converting the same input twice has to be byte-for-byte
// identical.
func SortedPorts(ports []kobject.Ports) []kobject.Ports {
	sorted := make([]kobject.Ports, len(ports))
	copy(sorted, ports)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.ContainerPort != b.ContainerPort {
			return a.ContainerPort < b.ContainerPort
		}
		if a.Protocol != b.Protocol {
			return a.Protocol < b.Protocol
		}
		if a.HostPort != b.HostPort {
			return a.HostPort < b.HostPort
		}
		if a.HostIP != b.HostIP {
			return a.HostIP < b.HostIP
		}
		return a.Mode < b.Mode
	})
	return sorted
}

// HasServicePorts reports whether at least one port should become a Kubernetes
// Service port. `mode: host` ports are rendered as container hostPorts instead.
func HasServicePorts(service kobject.ServiceConfig) bool {
	for _, port := range service.Port {
		if !port.IsHostMode() {
			return true
		}
	}
	return false
}

// HasPublishedPorts reports whether the service publishes at least one
// ingress-mode port to the host. Only such ports make a Service externally
// reachable, so only they can justify deriving NodePort.
func HasPublishedPorts(service kobject.ServiceConfig) bool {
	for _, port := range service.Port {
		if !port.IsHostMode() && port.HostPort != 0 {
			return true
		}
	}
	return false
}

// servicePortsOf returns the ports that belong to the Service, in sorted order.
func servicePortsOf(service kobject.ServiceConfig) []kobject.Ports {
	var ports []kobject.Ports
	for _, port := range SortedPorts(service.Port) {
		if !port.IsHostMode() {
			ports = append(ports, port)
		}
	}
	return ports
}

// servicePortGroups splits the service ports into the groups that become one
// generated Service each: a LoadBalancer gets a TCP and a UDP Service, everything
// else keeps its ports in a single Service. Port names only have to be unique
// inside a group, matching how the objects are actually emitted.
func servicePortGroups(service kobject.ServiceConfig, ports []kobject.Ports) [][]kobject.Ports {
	if service.ServiceType == string(api.ServiceTypeLoadBalancer) {
		var tcpGroup, udpGroup []kobject.Ports
		for _, port := range ports {
			if api.Protocol(port.Protocol) == api.ProtocolTCP {
				tcpGroup = append(tcpGroup, port)
			} else {
				udpGroup = append(udpGroup, port)
			}
		}
		var groups [][]kobject.Ports
		if len(tcpGroup) > 0 {
			groups = append(groups, tcpGroup)
		}
		if len(udpGroup) > 0 {
			groups = append(groups, udpGroup)
		}
		return groups
	}
	if len(ports) == 0 {
		return nil
	}
	return [][]kobject.Ports{ports}
}

// resolveServiceType applies the service type rules that depend on the
// conversion options and the controller. An explicit kompose.service.type
// label is never overridden by port derivation.
func resolveServiceType(service *kobject.ServiceConfig, opt kobject.ConvertOptions, add func(kobject.PortIssueSeverity, string, string)) {
	if !service.ServiceTypeExplicit && opt.DeriveServiceTypeFromPorts {
		if HasPublishedPorts(*service) {
			service.ServiceType = string(api.ServiceTypeNodePort)
			add(kobject.PortIssueInfo, kobject.PortCategoryDerivation,
				"derived service type NodePort from published ports because no kompose.service.type label is set (--derive-service-type-from-ports)")
		} else {
			service.ServiceType = string(api.ServiceTypeClusterIP)
			add(kobject.PortIssueInfo, kobject.PortCategoryDerivation,
				"derived service type ClusterIP because the service has no published ports")
		}
	}

	// StatefulSet requires a Headless Service. This predates port derivation and
	// is kept as is; a conflicting type is reported, not silently replaced,
	// unless kompose itself derived it (then there is nothing the user chose).
	if opt.Controller == StatefulStateController && service.ServiceType != serviceTypeHeadless {
		if service.ServiceTypeExplicit {
			add(kobject.PortIssueWarning, kobject.PortCategoryDerivation,
				fmt.Sprintf("controller statefulset requires a Headless Service; overriding service type %q", service.ServiceType))
		} else if service.ServiceType != "" {
			add(kobject.PortIssueInfo, kobject.PortCategoryDerivation,
				fmt.Sprintf("controller statefulset requires a Headless Service; replacing derived service type %q", service.ServiceType))
		}
		service.ServiceType = serviceTypeHeadless
	}
}

// ValidateServicePorts inspects the ports of one service and returns every
// problem it can find, so a single conversion reports them all instead of
// aborting on the first. It also resolves the effective Service type.
func ValidateServicePorts(service *kobject.ServiceConfig, opt kobject.ConvertOptions) []kobject.PortIssue {
	issues := append([]kobject.PortIssue{}, service.PortIssues...)
	add := func(severity kobject.PortIssueSeverity, category, message string) {
		issues = append(issues, kobject.PortIssue{
			Severity: severity,
			Category: category,
			Service:  service.Name,
			Message:  message,
		})
	}

	resolveServiceType(service, opt, add)

	var hostModeCount int
	for _, port := range service.Port {
		if port.IsHostMode() {
			hostModeCount++
		}
	}
	if hostModeCount > 0 {
		add(kobject.PortIssueInfo, kobject.PortCategoryMode,
			fmt.Sprintf("%d port(s) declared with mode: host are rendered as container hostPort and are not added to the Service", hostModeCount))
	}

	ports := servicePortsOf(*service)

	// Two container ports can map onto the same Service port (typically the same
	// published host port). Kubernetes rejects that on apply; catch it here.
	type portKey struct {
		protocol string
		port     int32
	}
	seenPorts := map[portKey]kobject.Ports{}
	for _, port := range ports {
		key := portKey{protocol: port.Protocol, port: exposedPort(port)}
		if previous, ok := seenPorts[key]; ok {
			add(kobject.PortIssueError, kobject.PortCategoryConflict,
				fmt.Sprintf("container ports %d and %d both map to service port %d/%s; use distinct published ports or drop one of them",
					previous.ContainerPort, port.ContainerPort, key.port, strings.ToLower(key.protocol)))
			continue
		}
		seenPorts[key] = port
	}

	// Names must be valid DNS-1123 labels and unique within each generated
	// Service. The 15-character limit from the URL-source container port
	// discussion applies to IANA_SVC_NAME (container port names), not to Service
	// port names; the API server validates Service ports with
	// ValidateDNS1123Label, so that is the rule mirrored here.
	for _, group := range servicePortGroups(*service, ports) {
		usedNames := map[string]kobject.Ports{}
		for _, port := range group {
			name := ServicePortName(port, group)
			if msg := validation.IsDNS1123Label(name); len(msg) > 0 {
				add(kobject.PortIssueError, kobject.PortCategoryConflict,
					fmt.Sprintf("generated service port name %q for container port %d is invalid: %s", name, port.ContainerPort, strings.Join(msg, "; ")))
				continue
			}
			if previous, ok := usedNames[name]; ok {
				add(kobject.PortIssueError, kobject.PortCategoryConflict,
					fmt.Sprintf("container ports %d and %d would both use service port name %q", previous.ContainerPort, port.ContainerPort, name))
				continue
			}
			usedNames[name] = port
		}
	}

	issues = append(issues, validateNodePorts(service, ports, opt)...)
	issues = append(issues, validateLoadBalancerPorts(service, ports)...)

	return issues
}

// validateNodePorts checks the legacy single-nodePort label and the per-port
// map against the Service type, the declared ports and each other.
func validateNodePorts(service *kobject.ServiceConfig, ports []kobject.Ports, opt kobject.ConvertOptions) []kobject.PortIssue {
	var issues []kobject.PortIssue
	add := func(category, message string) {
		issues = append(issues, kobject.PortIssue{
			Severity: kobject.PortIssueError,
			Category: category,
			Service:  service.Name,
			Message:  message,
		})
	}

	if service.NodePortPort != 0 && service.ServiceType != string(api.ServiceTypeNodePort) {
		add(kobject.PortCategoryConflict, "kompose.service.type must be nodeport when kompose.service.nodeport.port is set")
	}
	if service.NodePortPort != 0 && len(ports) != 1 {
		add(kobject.PortCategoryConflict, "cannot set kompose.service.nodeport.port when service has multiple ports; use kompose.service.nodeport.ports instead")
	}
	if service.NodePortPort != 0 && !opt.NodePortRange.Contains(service.NodePortPort) {
		add(kobject.PortCategoryRange, fmt.Sprintf("nodePort %d is outside the allowed range %s; adjust the range if the API server uses a non-default --service-node-port-range", service.NodePortPort, opt.NodePortRange.String()))
	}

	if len(service.NodePortPorts) == 0 {
		return issues
	}

	if service.ServiceType != string(api.ServiceTypeNodePort) {
		add(kobject.PortCategoryConflict, "kompose.service.type must be nodeport when kompose.service.nodeport.ports is set")
	}
	if service.NodePortPort != 0 {
		add(kobject.PortCategoryConflict, "kompose.service.nodeport.port and kompose.service.nodeport.ports cannot be used together")
	}

	containerPorts := map[int32]kobject.Ports{}
	for _, port := range service.Port {
		containerPorts[port.ContainerPort] = port
	}

	keys := make([]int, 0, len(service.NodePortPorts))
	for containerPort := range service.NodePortPorts {
		keys = append(keys, int(containerPort))
	}
	sort.Ints(keys)

	// Kubernetes validates nodePort uniqueness per (nodePort, protocol): the
	// same nodePort may serve TCP on one port and UDP on another, but never twice
	// with the same protocol.
	seen := map[string]int32{}
	for _, containerPort := range keys {
		nodePort := service.NodePortPorts[int32(containerPort)]
		port, ok := containerPorts[int32(containerPort)]
		if !ok {
			add(kobject.PortCategoryConflict, fmt.Sprintf("kompose.service.nodeport.ports references container port %d, which the service does not declare", containerPort))
			continue
		}
		if port.IsHostMode() {
			add(kobject.PortCategoryConflict, fmt.Sprintf("kompose.service.nodeport.ports assigns a nodePort to container port %d, but that port uses mode: host and is not part of the Service", containerPort))
			continue
		}
		if !opt.NodePortRange.Contains(nodePort) {
			add(kobject.PortCategoryRange, fmt.Sprintf("nodePort %d assigned to container port %d is outside the allowed range %s; adjust the range if the API server uses a non-default --service-node-port-range", nodePort, containerPort, opt.NodePortRange.String()))
			continue
		}
		key := fmt.Sprintf("%d/%s", nodePort, port.Protocol)
		if previous, ok := seen[key]; ok {
			add(kobject.PortCategoryConflict, fmt.Sprintf("container ports %d and %d both request nodePort %d/%s", previous, containerPort, nodePort, strings.ToLower(port.Protocol)))
			continue
		}
		seen[key] = int32(containerPort)
	}

	return issues
}

// validateLoadBalancerPorts rejects the combinations LoadBalancer Services
// cannot publish. It replaces the log.Fatalf that used to kill the process, so
// the problem is collected with every other port problem and reported once.
//
// A port number shared across protocols is legal for NodePort, but most cloud
// load balancers cannot publish the same frontend port twice. kompose splits a
// LoadBalancer into TCP and UDP Services, yet the two share one external
// address per cloud rule, so the combination is still rejected.
func validateLoadBalancerPorts(service *kobject.ServiceConfig, ports []kobject.Ports) []kobject.PortIssue {
	if service.ServiceType != string(api.ServiceTypeLoadBalancer) {
		return nil
	}

	var issues []kobject.PortIssue
	byPort := map[int32][]kobject.Ports{}
	for _, port := range ports {
		byPort[exposedPort(port)] = append(byPort[exposedPort(port)], port)
	}

	numbers := make([]int, 0, len(byPort))
	for number := range byPort {
		numbers = append(numbers, int(number))
	}
	sort.Ints(numbers)

	for _, number := range numbers {
		group := byPort[int32(number)]
		protocols := map[string]bool{}
		for _, port := range group {
			protocols[port.Protocol] = true
		}
		if len(protocols) < 2 {
			continue
		}
		issues = append(issues, kobject.PortIssue{
			Severity: kobject.PortIssueError,
			Category: kobject.PortCategoryConflict,
			Service:  service.Name,
			Message:  fmt.Sprintf("service port %d is used for both TCP and UDP; LoadBalancer Services cannot publish the same port number across protocols on most cloud providers", number),
		})
	}
	return issues
}

// servicePort returns the port number the Service exposes: the published host
// port when there is one, otherwise the container port.
func exposedPort(port kobject.Ports) int32 {
	if port.HostPort != 0 {
		return port.HostPort
	}
	return port.ContainerPort
}

// ServicePortName builds the deterministic name for a Service port: the exposed
// port number, suffixed with the lower-cased protocol when another port in the
// same generated Service exposes the same number. The first port to claim a
// number keeps the bare number, so the common case is unchanged.
//
// `peers` must be the ports of the Service the name is emitted in. LoadBalancer
// services are split into a TCP and a UDP Service, so the TCP and UDP halves of a
// shared port number live in different Services and must not suffix each other.
func ServicePortName(port kobject.Ports, peers []kobject.Ports) string {
	number := exposedPort(port)
	for _, other := range peers {
		if exposedPort(other) != number {
			continue
		}
		// The first peer exposing this number owns the bare name.
		if other.ID() == port.ID() {
			return strconv.Itoa(int(number))
		}
		// A different port claimed the number first, so this one is suffixed.
		return fmt.Sprintf("%s-%s", strconv.Itoa(int(number)), strings.ToLower(port.Protocol))
	}
	return strconv.Itoa(int(number))
}

// LogPortIssues logs and returns an error if any issue is fatal. Warnings and
// info are only logged: they do not change the exit status.
func LogPortIssues(issues []kobject.PortIssue) error {
	if len(issues) == 0 {
		return nil
	}
	var fatal []string
	for _, issue := range issues {
		switch issue.Severity {
		case kobject.PortIssueError:
			fatal = append(fatal, issue.String())
		case kobject.PortIssueWarning:
			log.Warn(issue.String())
		default:
			log.Info(issue.String())
		}
	}
	if len(fatal) > 0 {
		return fmt.Errorf("found %d port problem(s):\n  %s", len(fatal), strings.Join(fatal, "\n  "))
	}
	return nil
}

// ValidateKomposeObjectPorts runs port validation for every service, in sorted
// service-name order so diagnostics are deterministic.
func (k *Kubernetes) ValidateKomposeObjectPorts(komposeObject kobject.KomposeObject, opt kobject.ConvertOptions) []kobject.PortIssue {
	var issues []kobject.PortIssue
	for _, name := range SortedKeys(komposeObject.ServiceConfigs) {
		service := komposeObject.ServiceConfigs[name]
		issues = append(issues, ValidateServicePorts(&service, opt)...)
	}
	return issues
}
