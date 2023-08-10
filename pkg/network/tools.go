package network

import (
	"fmt"
	"regexp"
	"strings"
)

func getServiceName(s string) (string, error) {
	// Define the regular expression pattern to match "/api/" followed by the service name
	re := regexp.MustCompile(`/api/(\w+)`)

	// Find the first match in the input string
	matches := re.FindStringSubmatch(s)
	if len(matches) != 2 {
		return "", fmt.Errorf("no service name found in the input string")
	}

	// Extract the service name from the matched groups
	serviceName := matches[1]
	return serviceName, nil
}

func URLToService(url string) string {
	serviceName, err := getServiceName(url)
	if err != nil {
		return url
	}
	return serviceName
}

func ServiceToURL(service string) string {
	if strings.Contains(service, "/") {
		return service
	}
	return fmt.Sprintf("/api/%s", service)
}
