// +build e2e

/*
Copyright 2019 The Knative Authors

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

package v1

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"testing"

	pkgtest "knative.dev/pkg/test"
	"knative.dev/pkg/test/spoof"
	v1 "knative.dev/serving/pkg/apis/serving/v1"
	rtesting "knative.dev/serving/pkg/testing/v1"
	"knative.dev/serving/test"
	v1test "knative.dev/serving/test/v1"
)

func setServiceGenerateName(generateName string) rtesting.ServiceOption {
	return func(service *v1.Service) {
		service.ObjectMeta.GenerateName = generateName
	}
}

func setConfigurationGenerateName(generateName string) rtesting.ConfigOption {
	return func(config *v1.Configuration) {
		config.ObjectMeta.GenerateName = generateName
	}
}

func setRouteGenerateName(generateName string) rtesting.RouteOption {
	return func(route *v1.Route) {
		route.ObjectMeta.GenerateName = generateName
	}
}

// generateNamePrefix returns the object name to be used for testing, shorted to
// 44 characters to avoid #3236, as generateNames longer than 44 characters may cause
// some knative resources to never become ready.
func generateNamePrefix(t *testing.T) string {
	generateName := test.ObjectNameForTest(t) + "-"

	generateNameLength := len(generateName)
	if generateNameLength > 44 {
		generateNameLength = 44
	}
	return generateName[0:generateNameLength]
}

// validateName checks that a name generated using a generateName is valid. It checks
// 1. The generateName is a prefix of the name, but they are not equal
// 2. Any number of valid name characters (alphanumeric, -, and .) are added togenerateName to
//    create the value of name.
func validateName(generateName, name string) error {
	r := regexp.MustCompile("^" + regexp.QuoteMeta(generateName) + "[a-zA-Z0-9\\-.]+$")

	if !r.MatchString(name) {
		return fmt.Errorf("generated name = %q, want to match %q", name, r.String())
	}
	return nil
}

func canServeRequests(t *testing.T, clients *test.Clients, route *v1.Route) error {
	t.Logf("Route %s has a domain set in its status", route.Name)
	var url *url.URL
	err := v1test.CheckRouteState(
		clients.ServingClient,
		route.Name,
		func(r *v1.Route) (bool, error) {
			url = r.Status.URL.URL()
			return url.String() != "", nil
		},
	)
	if err != nil {
		return fmt.Errorf("route did not get assigned an URL: %w", err)
	}

	t.Logf("Route %s can serve the expected data at %s", route.Name, url)
	_, err = pkgtest.CheckEndpointState(
		context.Background(),
		clients.KubeClient,
		t.Logf,
		url,
		v1test.RetryingRouteInconsistency(spoof.MatchesAllOf(spoof.IsStatusOK, spoof.MatchesBody(test.HelloWorldText))),
		"CheckEndpointToServeText",
		test.ServingFlags.ResolvableDomain,
		test.AddRootCAtoTransport(context.Background(), t.Logf, clients, test.ServingFlags.HTTPS))
	if err != nil {
		return fmt.Errorf("the endpoint for Route %s at %s didn't serve the expected text %q: %w", route.Name, url, test.HelloWorldText, err)
	}

	return nil
}

// TestServiceGenerateName checks that knative Services MAY request names generated by
// the system using metadata.generateName. It ensures that knative Services created this way can become ready
// and serve requests.
func TestServiceGenerateName(t *testing.T) {
	t.Parallel()
	clients := test.Setup(t)

	generateName := generateNamePrefix(t)
	names := test.ResourceNames{
		Image: test.HelloWorld,
	}

	// Cleanup on test failure.
	test.EnsureTearDown(t, clients, &names)

	// Create the service using the generate name field. If the service does not become ready this will fail.
	t.Log("Creating new service with generateName", generateName)
	resources, err := v1test.CreateServiceReady(t, clients, &names, setServiceGenerateName(generateName))
	if err != nil {
		t.Fatalf("Failed to create service with generateName %s: %v", generateName, err)
	}

	// Ensure that the name given to the service is generated from the generateName field.
	t.Log("When the service is created, the name is generated using the provided generateName")
	if err := validateName(generateName, names.Service); err != nil {
		t.Errorf("Illegal name generated for service %s: %v", names.Service, err)
	}

	// Ensure that the service can serve requests
	err = canServeRequests(t, clients, resources.Route)
	if err != nil {
		t.Errorf("Service %s could not serve requests: %v", names.Service, err)
	}
}

// TestRouteAndConfiguration checks that both routes and configurations MAY request names generated by
// the system using metadata.generateName. It ensures that routes and configurations created this way both:
// 1. Become ready
// 2. Can serve requests.
func TestRouteAndConfigGenerateName(t *testing.T) {
	t.Parallel()
	clients := test.Setup(t)

	generateName := generateNamePrefix(t)
	names := test.ResourceNames{
		Image: test.HelloWorld,
	}

	test.EnsureTearDown(t, clients, &names)

	t.Log("Creating new configuration with generateName", generateName)
	config, err := v1test.CreateConfiguration(t, clients, names, setConfigurationGenerateName(generateName))
	if err != nil {
		t.Fatalf("Failed to create configuration with generateName %s: %v", generateName, err)
	}
	names.Config = config.Name

	// Ensure the associated revision is created. This also checks that the configuration becomes ready.
	t.Log("The configuration will be updated with the name of the associated Revision once it is created.")
	names.Revision, err = v1test.WaitForConfigLatestUnpinnedRevision(clients, names)
	if err != nil {
		t.Fatalf("Configuration %s was not updated with the new revision: %v", names.Config, err)
	}

	// Ensure that the name given to the configuration is generated from the generate name field.
	t.Log("When the configuration is created, the name is generated using the provided generateName")
	if err := validateName(generateName, names.Config); err != nil {
		t.Errorf("Illegal name generated for configuration %s: %v", names.Config, err)
	}

	// Create a route that maps to the revision created by the configuration above
	t.Log("Create new Route with generateName", generateName)
	route, err := v1test.CreateRoute(t, clients, names, setRouteGenerateName(generateName))
	if err != nil {
		t.Fatalf("Failed to create route with generateName %s: %v", generateName, err)
	}
	names.Route = route.Name

	t.Log("When the route is created, it will become ready")
	if err := v1test.WaitForRouteState(clients.ServingClient, names.Route, v1test.IsRouteReady, "RouteIsReady"); err != nil {
		t.Fatalf("Error waiting for the route %s to become ready: %v", names.Route, err)
	}

	// Ensure that the name given to the route is generated from the generate name field
	t.Log("When the route is created, the name is generated using the provided generateName")
	if err := validateName(generateName, names.Route); err != nil {
		t.Errorf("Illegal name generated for route %s: %v", names.Route, err)
	}

	// Ensure that the generated route endpoint can serve requests
	if err := canServeRequests(t, clients, route); err != nil {
		t.Errorf("Configuration %s with Route %s could not serve requests: %v", names.Config, names.Route, err)
	}
}
