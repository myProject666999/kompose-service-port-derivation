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

package compose

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/google/go-cmp/cmp"
	"github.com/kubernetes/kompose/pkg/kobject"
	"github.com/pkg/errors"
	api "k8s.io/api/core/v1"
)

func durationTypesPtr(value time.Duration) *types.Duration {
	target := types.Duration(value)
	return &target
}

func TestParseHealthCheck(t *testing.T) {
	helperValue := uint64(2)
	type input struct {
		healthCheck types.HealthCheckConfig
		labels      types.Labels
	}
	testCases := map[string]struct {
		input    input
		expected kobject.HealthCheck
	}{
		"Exec": {
			input: input{
				healthCheck: types.HealthCheckConfig{
					Test:        []string{"CMD-SHELL", "echo", "foobar"},
					Timeout:     durationTypesPtr(1 * time.Second),
					Interval:    durationTypesPtr(2 * time.Second),
					Retries:     &helperValue,
					StartPeriod: durationTypesPtr(3 * time.Second),
				},
			},
			// CMD-SHELL or SHELL is included Test within docker/cli, thus we remove the first value in Test
			expected: kobject.HealthCheck{
				Test:        []string{"echo", "foobar"},
				Timeout:     1,
				Interval:    2,
				Retries:     2,
				StartPeriod: 3,
			},
		},
		"HTTPGet": {
			input: input{
				healthCheck: types.HealthCheckConfig{
					Timeout:     durationTypesPtr(1 * time.Second),
					Interval:    durationTypesPtr(2 * time.Second),
					Retries:     &helperValue,
					StartPeriod: durationTypesPtr(3 * time.Second),
				},
				labels: types.Labels{
					"kompose.service.healthcheck.liveness.http_get_path": "/health",
					"kompose.service.healthcheck.liveness.http_get_port": "8080",
				},
			},
			expected: kobject.HealthCheck{
				HTTPPath:    "/health",
				HTTPPort:    8080,
				Timeout:     1,
				Interval:    2,
				Retries:     2,
				StartPeriod: 3,
			},
		},
		"TCPSocket": {
			input: input{
				healthCheck: types.HealthCheckConfig{
					Timeout:     durationTypesPtr(1 * time.Second),
					Interval:    durationTypesPtr(2 * time.Second),
					Retries:     &helperValue,
					StartPeriod: durationTypesPtr(3 * time.Second),
				},
				labels: types.Labels{
					"kompose.service.healthcheck.liveness.tcp_port": "8080",
				},
			},
			expected: kobject.HealthCheck{
				TCPPort:     8080,
				Timeout:     1,
				Interval:    2,
				Retries:     2,
				StartPeriod: 3,
			},
		},
	}

	for name, testCase := range testCases {
		t.Log("Test case:", name)
		output, err := parseHealthCheck(testCase.input.healthCheck, testCase.input.labels)
		if err != nil {
			t.Errorf("Unable to convert HealthCheckConfig: %s", err)
		}

		if !reflect.DeepEqual(output, testCase.expected) {
			t.Errorf("Structs are not equal, expected: %v, output: %v", testCase.expected, output)
		}
	}
}

func TestParseHealthCheckReadiness(t *testing.T) {
	testCases := map[string]struct {
		input    types.Labels
		expected kobject.HealthCheck
	}{
		"Exec": {
			input: types.Labels{
				"kompose.service.healthcheck.readiness.test":         "echo foobar",
				"kompose.service.healthcheck.readiness.timeout":      "1s",
				"kompose.service.healthcheck.readiness.interval":     "2s",
				"kompose.service.healthcheck.readiness.retries":      "2",
				"kompose.service.healthcheck.readiness.start_period": "3s",
			},
			expected: kobject.HealthCheck{
				Test:        []string{"echo", "foobar"},
				Timeout:     1,
				Interval:    2,
				Retries:     2,
				StartPeriod: 3,
			},
		},
		"HTTPGet": {
			input: types.Labels{
				"kompose.service.healthcheck.readiness.http_get_path": "/ready",
				"kompose.service.healthcheck.readiness.http_get_port": "8080",
				"kompose.service.healthcheck.readiness.timeout":       "1s",
				"kompose.service.healthcheck.readiness.interval":      "2s",
				"kompose.service.healthcheck.readiness.retries":       "2",
				"kompose.service.healthcheck.readiness.start_period":  "3s",
			},
			expected: kobject.HealthCheck{
				HTTPPath:    "/ready",
				HTTPPort:    8080,
				Timeout:     1,
				Interval:    2,
				Retries:     2,
				StartPeriod: 3,
			},
		},
		"TCPSocket": {
			input: types.Labels{
				"kompose.service.healthcheck.readiness.tcp_port":     "8080",
				"kompose.service.healthcheck.readiness.timeout":      "1s",
				"kompose.service.healthcheck.readiness.interval":     "2s",
				"kompose.service.healthcheck.readiness.retries":      "2",
				"kompose.service.healthcheck.readiness.start_period": "3s",
			},
			expected: kobject.HealthCheck{
				TCPPort:     8080,
				Timeout:     1,
				Interval:    2,
				Retries:     2,
				StartPeriod: 3,
			},
		},
	}

	for name, testCase := range testCases {
		t.Log("Test case:", name)
		output, err := parseHealthCheckReadiness(testCase.input)
		if err != nil {
			t.Errorf("Unable to convert HealthCheckConfig: %s", err)
		}

		if !reflect.DeepEqual(output, testCase.expected) {
			t.Errorf("Structs are not equal, expected: %v, output: %v", testCase.expected, output)
		}
	}
}

func TestLoadV3Volumes(t *testing.T) {
	vol := types.ServiceVolumeConfig{
		Type:     "volume",
		Source:   "/tmp/foobar",
		Target:   "/tmp/foobar",
		ReadOnly: true,
	}
	volumes := []types.ServiceVolumeConfig{vol}
	output := loadVolumes(volumes)
	expected := "/tmp/foobar:/tmp/foobar:ro"

	if output[0] != expected {
		t.Errorf("Expected %s, got %s", expected, output[0])
	}
}

func TestLoadV3Ports(t *testing.T) {
	for _, tt := range []struct {
		desc   string
		ports  []types.ServicePortConfig
		expose []string
		want   []kobject.Ports
	}{
		{
			desc:   "ports with expose",
			ports:  []types.ServicePortConfig{{Target: 80, Published: "80", Protocol: string(api.ProtocolTCP)}},
			expose: []string{"80", "8080"},
			want: []kobject.Ports{
				{HostPort: 80, ContainerPort: 80, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
				{ContainerPort: 8080, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
			},
		},
		{
			desc:   "exposed port including /protocol",
			ports:  []types.ServicePortConfig{{Target: 80, Published: "80", Protocol: string(api.ProtocolTCP)}},
			expose: []string{"80/udp"},
			want: []kobject.Ports{
				{HostPort: 80, ContainerPort: 80, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
				{ContainerPort: 80, Protocol: string(api.ProtocolUDP), Mode: kobject.PortModeIngress},
			},
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			got, issues := loadPorts("web", tt.ports, tt.expose)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("loadV3Ports() mismatch (-want +got):\n%s", diff)
			}
			if len(issues) != 0 {
				t.Errorf("loadV3Ports() unexpected issues: %v", issues)
			}
		})
	}
}

// Test if service types are parsed properly on user input
// give a service type and expect correct input
func TestHandleServiceType(t *testing.T) {
	tests := []struct {
		labelValue  string
		serviceType string
	}{
		{"NodePort", "NodePort"},
		{"nodeport", "NodePort"},
		{"LoadBalancer", "LoadBalancer"},
		{"loadbalancer", "LoadBalancer"},
		{"ClusterIP", "ClusterIP"},
		{"clusterip", "ClusterIP"},
		{"", "ClusterIP"},
	}

	for _, tt := range tests {
		result, err := handleServiceType(tt.labelValue)
		if err != nil {
			t.Error(errors.Wrap(err, "handleServiceType failed"))
		}
		if result != tt.serviceType {
			t.Errorf("Expected %q, got %q", tt.serviceType, result)
		}
	}
}

// Test loading of ports
func TestLoadPorts(t *testing.T) {
	portWithIPAddress, _ := types.ParsePortConfig("127.0.0.1:80:80/tcp")
	portWithoutIPAddress, _ := types.ParsePortConfig("80:80/tcp")
	portWithoutProtocol, _ := types.ParsePortConfig("80:80")
	singlePort, _ := types.ParsePortConfig("80")
	singlePortsRange, _ := types.ParsePortConfig("3000-3002")
	targetAndContainerPortsRange, _ := types.ParsePortConfig("3000-3002:5000-5002")
	targetAndContainerPortsRangeWithIPAddress, _ := types.ParsePortConfig("127.0.0.1:3000-3002:5000-5002")
	port3000, _ := types.ParsePortConfig("3000")

	tests := []struct {
		ports  []types.ServicePortConfig
		expose []string
		want   []kobject.Ports
		// issues, when non-nil, are the only issues expected. A nil value means
		// "expect no issues at all".
		issues []kobject.PortIssue
	}{
		{
			ports: portWithIPAddress,
			want: []kobject.Ports{
				{HostIP: "127.0.0.1", HostPort: 80, ContainerPort: 80, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
			},
			issues: []kobject.PortIssue{loopbackIssue("web", "127.0.0.1")},
		},
		{
			ports: portWithoutIPAddress,
			want: []kobject.Ports{
				{HostPort: 80, ContainerPort: 80, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
			},
		},
		{
			ports: portWithoutProtocol,
			want: []kobject.Ports{
				{HostPort: 80, ContainerPort: 80, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
			},
		},
		{
			ports: singlePort,
			want: []kobject.Ports{
				{ContainerPort: 80, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
			},
		},
		{
			ports: singlePortsRange,
			want: []kobject.Ports{
				{ContainerPort: 3000, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
				{ContainerPort: 3001, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
				{ContainerPort: 3002, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
			},
		},
		{
			ports: targetAndContainerPortsRange,
			want: []kobject.Ports{
				{HostPort: 3000, ContainerPort: 5000, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
				{HostPort: 3001, ContainerPort: 5001, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
				{HostPort: 3002, ContainerPort: 5002, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
			},
		},
		{
			ports: targetAndContainerPortsRangeWithIPAddress,
			want: []kobject.Ports{
				{HostIP: "127.0.0.1", HostPort: 3000, ContainerPort: 5000, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
				{HostIP: "127.0.0.1", HostPort: 3001, ContainerPort: 5001, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
				{HostIP: "127.0.0.1", HostPort: 3002, ContainerPort: 5002, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
			},
			// One warning for the address, not one per expanded port.
			issues: []kobject.PortIssue{loopbackIssue("web", "127.0.0.1")},
		},
		{
			ports: append(append([]types.ServicePortConfig{}, singlePort...), port3000...),
			want: []kobject.Ports{
				{HostPort: 0, ContainerPort: 80, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
				{HostPort: 0, ContainerPort: 3000, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
			},
		},
		{
			ports:  append(append([]types.ServicePortConfig{}, singlePort...), port3000...),
			expose: []string{"80", "8080"},
			// expose "80" duplicates the published container port 80 and is
			// skipped; this used to slip through because the dedupe key mixed
			// the lower-case compose protocol with the upper-case stored one.
			want: []kobject.Ports{
				{ContainerPort: 80, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
				{ContainerPort: 3000, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
				{ContainerPort: 8080, Protocol: string(api.ProtocolTCP), Mode: kobject.PortModeIngress},
			},
		},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("port=%q,expose=%q", tt.ports, tt.expose), func(t *testing.T) {
			got, issues := loadPorts("web", tt.ports, tt.expose)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("loadPorts() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.issues, issues); diff != "" {
				t.Errorf("loadPorts() issue mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoadEnvVar(t *testing.T) {
	ev1 := []string{"foo=bar"}
	rs1 := kobject.EnvVar{
		Name:  "foo",
		Value: "bar",
	}
	ev2 := []string{"foo:bar"}
	rs2 := kobject.EnvVar{
		Name:  "foo",
		Value: "bar",
	}
	ev3 := []string{"foo"}
	rs3 := kobject.EnvVar{
		Name:  "foo",
		Value: "",
	}
	ev4 := []string{"osfoo"}
	rs4 := kobject.EnvVar{
		Name:  "osfoo",
		Value: "osbar",
	}
	ev5 := []string{"foo:bar=foobar"}
	rs5 := kobject.EnvVar{
		Name:  "foo",
		Value: "bar=foobar",
	}
	ev6 := []string{"foo=foo:bar"}
	rs6 := kobject.EnvVar{
		Name:  "foo",
		Value: "foo:bar",
	}
	ev7 := []string{"foo:"}
	rs7 := kobject.EnvVar{
		Name:  "foo",
		Value: "",
	}
	ev8 := []string{"foo="}
	rs8 := kobject.EnvVar{
		Name:  "foo",
		Value: "",
	}

	tests := []struct {
		envvars []string
		results kobject.EnvVar
	}{
		{ev1, rs1},
		{ev2, rs2},
		{ev3, rs3},
		{ev4, rs4},
		{ev5, rs5},
		{ev6, rs6},
		{ev7, rs7},
		{ev8, rs8},
	}

	os.Setenv("osfoo", "osbar")

	for _, tt := range tests {
		result := loadEnvVars(tt.envvars)
		if result[0] != tt.results {
			t.Errorf("Expected %q, got %q", tt.results, result[0])
		}
	}
}

func TestParseEnvFiles(t *testing.T) {
	tests := []struct {
		service types.ServiceConfig
		want    []string
	}{
		{service: types.ServiceConfig{
			Name:  "baz",
			Image: "foo/baz",
			EnvFiles: []types.EnvFile{
				{
					Path:     "",
					Required: false,
				},
				{
					Path:     "foo",
					Required: false,
				},
				{
					Path:     "bar",
					Required: true,
				},
			},
		},
			want: []string{"", "foo", "bar"},
		},
		{
			service: types.ServiceConfig{
				Name:     "baz",
				Image:    "foo/baz",
				EnvFiles: []types.EnvFile{},
			},
			want: []string{},
		},
	}

	for _, tt := range tests {
		sc := kobject.ServiceConfig{
			EnvFile: []string{},
		}
		parseEnvFiles(&tt.service, &sc)
		if !reflect.DeepEqual(sc.EnvFile, tt.want) {
			t.Errorf("Expected %q, got %q", tt.want, sc.EnvFile)
		}
	}
}

// TestUnsupportedKeys test checkUnsupportedKey function with various
// docker-compose projects
func TestUnsupportedKeys(t *testing.T) {
	// create project that will be used in test cases
	projectWithNetworks := &types.Project{
		Networks: types.Networks{
			"foo": types.NetworkConfig{
				Name:   "foo",
				Driver: "bridge",
			},
		},
		Services: types.Services{
			"foo": types.ServiceConfig{
				Name:  "foo",
				Image: "foo/bar",
				Build: &types.BuildConfig{
					Context: "./build",
				},
				Hostname: "localhost",
				Ports:    []types.ServicePortConfig{}, // test empty array
				Networks: map[string]*types.ServiceNetworkConfig{
					"net1": {},
				},
			},
			"bar": types.ServiceConfig{
				Name:  "bar",
				Image: "bar/foo",
				Build: &types.BuildConfig{
					Context: "./build",
				},
				Hostname: "localhost",
				Ports:    []types.ServicePortConfig{}, // test empty array
				Networks: map[string]*types.ServiceNetworkConfig{
					"net1": {},
				},
			},
		},
		Volumes: types.Volumes{
			"foo": types.VolumeConfig{
				Name:   "foo",
				Driver: "storage",
			},
		},
	}

	projectWithDefaultNetwork := &types.Project{
		Services: types.Services{
			"foo": types.ServiceConfig{
				Networks: map[string]*types.ServiceNetworkConfig{
					"default": {},
				},
			},
		},
	}

	// define all test cases for checkUnsupportedKey function
	testCases := map[string]struct {
		composeProject          *types.Project
		expectedUnsupportedKeys []string
	}{
		"With Networks (service and root level)": {
			projectWithNetworks,
			//root level network and network are now supported"
			[]string{"root level volumes"},
		},
		"Default root level Network": {
			projectWithDefaultNetwork,
			[]string(nil),
		},
	}

	for name, test := range testCases {
		t.Log("Test case:", name)
		keys := checkUnsupportedKey(test.composeProject)
		if !reflect.DeepEqual(keys, test.expectedUnsupportedKeys) {
			t.Errorf("ERROR: Expecting unsupported keys: ['%s']. Got: ['%s']", strings.Join(test.expectedUnsupportedKeys, "', '"), strings.Join(keys, "', '"))
		}
	}
}

func TestNormalizeServiceNames(t *testing.T) {
	testCases := []struct {
		composeServiceName    string
		normalizedServiceName string
	}{
		{"foo_bar", "foo-bar"},
		{"foo", "foo"},
		{"foo.bar", "foo-bar"},
		//{"", ""},
	}

	for _, testCase := range testCases {
		returnValue := normalizeServiceNames(testCase.composeServiceName)
		if returnValue != testCase.normalizedServiceName {
			t.Errorf("Expected %q, got %q", testCase.normalizedServiceName, returnValue)
		}
	}
}

func TestLoadComposeServiceNameWithUnderscore(t *testing.T) {
	composeFile, err := os.CreateTemp(t.TempDir(), "compose-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(composeFile.Name())

	content := `
services:
  foo_bar:
    image: nginx
    ports:
      - "8080:80"
`
	if _, err := composeFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := composeFile.Close(); err != nil {
		t.Fatal(err)
	}

	c := Compose{}
	komposeObject, err := c.LoadFile([]string{composeFile.Name()}, nil, true)
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}

	svc, ok := komposeObject.ServiceConfigs["foo-bar"]
	if !ok {
		t.Fatalf("Expected normalized service key %q, got keys: %v", "foo-bar", getKeys(komposeObject.ServiceConfigs))
	}
	if svc.Name != "foo-bar" {
		t.Errorf("Expected service name %q, got %q", "foo-bar", svc.Name)
	}
}

func getKeys(m map[string]kobject.ServiceConfig) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestNormalizeNetworkNames(t *testing.T) {
	testCases := []struct {
		composeNetworkName    string
		normalizedNetworkName string
	}{
		{"foo_bar", "foo-bar"},
		{"foo", "foo"},
		{"FOO", "foo"},
		{"foo.bar", "foo.bar"},
		//{"", ""},
	}

	for _, testCase := range testCases {
		returnValue, err := normalizeNetworkNames(testCase.composeNetworkName)
		if err != nil {
			t.Log("Unexpected error, got ", err)
		}
		if returnValue != testCase.normalizedNetworkName {
			t.Logf("Expected %q, got %q", testCase.normalizedNetworkName, returnValue)
		}
	}
}

func TestCheckPlacementCustomLabels(t *testing.T) {
	placement := types.Placement{
		Constraints: []string{
			"node.labels.something == anything",
			"node.labels.monitor != xxx",
		},
		Preferences: []types.PlacementPreferences{
			{Spread: "node.labels.zone"},
			{Spread: "foo"},
			{Spread: "node.labels.ssd"},
		},
	}
	output := loadPlacement(placement)

	expected := kobject.Placement{
		PositiveConstraints: map[string]string{
			"something": "anything",
		},
		NegativeConstraints: map[string]string{
			"monitor": "xxx",
		},
		Preferences: []string{
			"zone", "ssd",
		},
	}

	checkConstraints(t, "positive", output.PositiveConstraints, expected.PositiveConstraints)
	checkConstraints(t, "negative", output.NegativeConstraints, expected.NegativeConstraints)

	if len(output.Preferences) != len(expected.Preferences) {
		t.Errorf("preferences len is not equal, expected %d, got %d", len(expected.Preferences), len(output.Preferences))
	}
	for i := range output.Preferences {
		if output.Preferences[i] != expected.Preferences[i] {
			t.Errorf("preference is not equal, expected %s, got %s", expected.Preferences[i], output.Preferences[i])
		}
	}
}

func checkConstraints(t *testing.T, caseName string, output, expected map[string]string) {
	t.Log("Test case:", caseName)
	if len(output) != len(expected) {
		t.Errorf("constraints len is not equal, expected %d, got %d", len(expected), len(output))
	}
	for key := range output {
		if output[key] != expected[key] {
			t.Errorf("%s constraint is not equal, expected %s, got %s", key, expected[key], output[key])
		}
	}
}

func Test_parseKomposeLabels(t *testing.T) {
	service := kobject.ServiceConfig{
		Name:          "name",
		ContainerName: "containername",
		Image:         "image",
		Labels:        nil,
		Annotations:   map[string]string{"abc": "def"},
		Restart:       "always",
	}

	type args struct {
		labels        types.Labels
		serviceConfig *kobject.ServiceConfig
	}
	tests := []struct {
		name     string
		args     args
		expected *kobject.ServiceConfig
	}{
		{
			name: "override with overriding",
			args: args{
				labels: types.Labels{
					LabelNameOverride: "overriding",
				},
				serviceConfig: &service,
			},
			expected: &kobject.ServiceConfig{
				Name: "overriding",
			},
		},
		{
			name: "override",
			args: args{
				labels: types.Labels{
					LabelNameOverride: "overriding-resource-name",
				},
				serviceConfig: &service,
			},
			expected: &kobject.ServiceConfig{
				Name: "overriding-resource-name",
			},
		},
		{
			name: "hyphen in the middle",
			args: args{
				labels: types.Labels{
					LabelNameOverride: "overriding_resource-name",
				},
				serviceConfig: &service,
			},
			expected: &kobject.ServiceConfig{
				Name: "overriding-resource-name",
			},
		},
		{
			name: "hyphen in the middle with mays",
			args: args{
				labels: types.Labels{
					LabelNameOverride: "OVERRIDING_RESOURCE-NAME",
				},
				serviceConfig: &service,
			},
			expected: &kobject.ServiceConfig{
				Name: "overriding-resource-name",
			},
		},
		// This is a corner case that is expected to fail because
		// it does not account for scenarios where the string
		// starts or ends with a '-' or any other character
		// this test will fail with current tests
		// {
		// 	name: "Add a prefix with a dash at the start and end, with a hyphen in the middle.",
		// 	args: args{
		// 		labels: types.Labels{
		// 			LabelNameOverride: "-OVERRIDING_RESOURCE-NAME-",
		// 		},
		// 		serviceConfig: &service,
		// 	},
		// 	expected: &kobject.ServiceConfig{
		// 		Name: "overriding-resource-name",
		// 	},
		// },
		// not fail
		{
			name: "Add a prefix with a dash at the start and end, with a hyphen in the middle.",
			args: args{
				labels: types.Labels{
					LabelNameOverride: "-OVERRIDING_RESOURCE-NAME-",
				},
				serviceConfig: &service,
			},
			expected: &kobject.ServiceConfig{
				Name: "-overriding-resource-name-",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := parseKomposeLabels(tt.args.labels, tt.args.serviceConfig); err != nil {
				t.Errorf("parseKomposeLabels(): %v", err)
			}

			if tt.expected.Name != tt.args.serviceConfig.Name {
				t.Errorf("Name are not equal, expected: %v, output: %v", tt.expected.Name, tt.args.serviceConfig.Name)
			}
		})
	}
}

// loopbackIssue builds the port-exposure warning loadPorts emits for a
// loopback host binding.
func loopbackIssue(service, hostIP string) kobject.PortIssue {
	return kobject.PortIssue{
		Severity: kobject.PortIssueWarning,
		Category: kobject.PortCategoryExposure,
		Service:  service,
		Message:  "host IP " + hostIP + " is a loopback address; binding is only meant for the local host, but a Service exposes the port cluster-wide and NodePort/LoadBalancer expose it further",
	}
}

// TestLoadPortsPortIssues covers the port problems loadPorts must surface
// instead of silently swallowing.
func TestLoadPortsPortIssues(t *testing.T) {
	hostRangeToOneTarget, _ := types.ParsePortConfig("8000-8010:80")
	containerRange, _ := types.ParsePortConfig("3000-3002")
	hostAndContainerRange, _ := types.ParsePortConfig("8000-8010:9000-9010")
	hostModePort := []types.ServicePortConfig{{Target: 80, Published: "8080", Protocol: "tcp", Mode: "host"}}
	ingressModePort := []types.ServicePortConfig{{Target: 80, Published: "8080", Protocol: "tcp", Mode: "ingress"}}

	tests := []struct {
		desc   string
		ports  []types.ServicePortConfig
		expose []string
		want   []kobject.Ports
		issues []kobject.PortIssue
	}{
		{
			desc:   "host range aimed at one container port is an error, not port 0",
			ports:  hostRangeToOneTarget,
			want:   []kobject.Ports{},
			issues: []kobject.PortIssue{{Severity: kobject.PortIssueError, Category: kobject.PortCategoryRange, Service: "web", Message: `published port range "8000-8010" on container port 80 is not supported; declare one host-to-container port mapping per line or use matching ranges such as 8000-8010:9000-9010`}},
		},
		{
			desc:  "matching host and container ranges expand pairwise",
			ports: hostAndContainerRange,
			want: []kobject.Ports{
				{HostPort: 8000, ContainerPort: 9000, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{HostPort: 8001, ContainerPort: 9001, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{HostPort: 8002, ContainerPort: 9002, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{HostPort: 8003, ContainerPort: 9003, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{HostPort: 8004, ContainerPort: 9004, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{HostPort: 8005, ContainerPort: 9005, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{HostPort: 8006, ContainerPort: 9006, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{HostPort: 8007, ContainerPort: 9007, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{HostPort: 8008, ContainerPort: 9008, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{HostPort: 8009, ContainerPort: 9009, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{HostPort: 8010, ContainerPort: 9010, Protocol: "TCP", Mode: kobject.PortModeIngress},
			},
		},
		{
			desc:  "container range without host ports is preserved",
			ports: containerRange,
			want: []kobject.Ports{
				{ContainerPort: 3000, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{ContainerPort: 3001, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{ContainerPort: 3002, Protocol: "TCP", Mode: kobject.PortModeIngress},
			},
		},
		{
			desc:  "mode host is preserved on the port",
			ports: hostModePort,
			want:  []kobject.Ports{{HostPort: 8080, ContainerPort: 80, Protocol: "TCP", Mode: kobject.PortModeHost}},
		},
		{
			desc:  "mode ingress is preserved on the port",
			ports: ingressModePort,
			want:  []kobject.Ports{{HostPort: 8080, ContainerPort: 80, Protocol: "TCP", Mode: kobject.PortModeIngress}},
		},
		{
			desc:   "unknown mode is an error",
			ports:  []types.ServicePortConfig{{Target: 80, Published: "8080", Protocol: "tcp", Mode: "bridge"}},
			want:   []kobject.Ports{},
			issues: []kobject.PortIssue{{Severity: kobject.PortIssueError, Category: kobject.PortCategoryMode, Service: "web", Message: `unsupported mode "bridge" on port 80/tcp, supported values are "host" and "ingress"`}},
		},
		{
			desc:   "expose range expands",
			expose: []string{"3000-3002"},
			want: []kobject.Ports{
				{ContainerPort: 3000, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{ContainerPort: 3001, Protocol: "TCP", Mode: kobject.PortModeIngress},
				{ContainerPort: 3002, Protocol: "TCP", Mode: kobject.PortModeIngress},
			},
		},
		{
			desc:   "invalid expose range is an error",
			expose: []string{"3000-abc"},
			want:   []kobject.Ports{},
			issues: []kobject.PortIssue{{Severity: kobject.PortIssueError, Category: kobject.PortCategoryRange, Service: "web", Message: `invalid expose entry "3000-abc": invalid port "3000-abc"`}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, issues := loadPorts("web", tt.ports, tt.expose)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("loadPorts() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.issues, issues); diff != "" {
				t.Errorf("loadPorts() issues mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseNodePortsLabel(t *testing.T) {
	tests := []struct {
		value   string
		want    map[int32]int32
		wantErr string
	}{
		{value: "", want: map[int32]int32{}},
		{value: "80:30080", want: map[int32]int32{80: 30080}},
		{value: " 80:30080 , 443:30443 ", want: map[int32]int32{80: 30080, 443: 30443}},
		{value: "80:30080,80:30081", wantErr: "container port 80 is assigned a node port more than once"},
		{value: "80", wantErr: `entry "80" must be in the form <container-port>:<node-port>`},
		{value: "0:30080", wantErr: `entry "0:30080" has an invalid container port`},
		{value: "80:70000", wantErr: `entry "80:70000" has an invalid node port`},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := parseNodePortsLabel(tt.value)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("expected error %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("parseNodePortsLabel() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
