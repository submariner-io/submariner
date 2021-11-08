/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

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

package endpoint

import (
	"context"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"
)

type publicIPResolverFunction func(clientset kubernetes.Interface, namespace, value string) (string, error)

var publicIPMethods = map[string]publicIPResolverFunction{
	v1.API:          publicAPI,
	v1.IPv4:         publicIP,
	v1.LoadBalancer: publicLoadBalancerIP,
	v1.DNS:          publicDNSIP,
}

var IPv4RE = regexp.MustCompile(`(?:\d{1,3}\.){3}\d{1,3}`)

func getPublicIP(submSpec types.SubmarinerSpecification, k8sClient kubernetes.Interface, backendConfig map[string]string) (string, error) {
	config, ok := backendConfig[v1.PublicIP]
	if !ok {
		if submSpec.PublicIP != "" {
			config = submSpec.PublicIP
		} else {
			config = "api:api.ipify.org,api:api.my-ip.io/ip,api:ip4.seeip.org"
		}
	}

	resolvers := strings.Split(config, ",")
	for _, resolver := range resolvers {
		resolver = strings.Trim(resolver, " ")
		parts := strings.Split(resolver, ":")
		if len(parts) != 2 {
			return "", errors.Errorf("invalid format for %q annotation: %q", v1.GatewayConfigPrefix+v1.PublicIP, config)
		}

		method, ok := publicIPMethods[parts[0]]
		if !ok {
			return "", errors.Errorf("unknown resolver %q in %q annotation: %q", parts[0], v1.GatewayConfigPrefix+v1.PublicIP, config)
		}

		ip, err := method(k8sClient, submSpec.Namespace, parts[1])
		if err == nil {
			return ip, nil
		}

		// If this resolved failed we log it, but we fall back to the next one
		klog.Errorf("Error resolving public IP with resolver %s : %s", resolver, err.Error())
	}

	if len(resolvers) > 0 {
		return "", errors.Errorf("Unable to resolve public IP by any of the resolver methods: %s", resolvers)
	}

	return "", nil
}

func publicAPI(clientset kubernetes.Interface, namespace, value string) (string, error) {
	url := "https://" + value

	//nolint:gosec // we really need to get from a non-predefined const URL
	response, err := http.Get(url)
	if err != nil {
		return "", errors.Wrapf(err, "retrieving public IP from %s", url)
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", errors.Wrapf(err, "reading API response from %s", url)
	}

	return firstIPv4InString(string(body))
}

func publicIP(clientset kubernetes.Interface, namespace, value string) (string, error) {
	return firstIPv4InString(value)
}

var loadBalancerRetryConfig = wait.Backoff{
	Cap:      6 * time.Minute,
	Duration: 5 * time.Second,
	Factor:   1.2,
	Steps:    24,
}

func publicLoadBalancerIP(clientset kubernetes.Interface, namespace, loadBalancerName string) (string, error) {
	ip := ""

	err := retry.OnError(loadBalancerRetryConfig, func(err error) bool {
		klog.Infof("Waiting for LoadBalancer to be ready: %s", err)
		return true
	}, func() error {
		service, err := clientset.CoreV1().Services(namespace).Get(context.TODO(), loadBalancerName, metav1.GetOptions{})
		if err != nil {
			return errors.Wrapf(err, "error getting service %q for the public IP address", loadBalancerName)
		}

		if len(service.Status.LoadBalancer.Ingress) < 1 {
			return errors.Errorf("service %q doesn't contain any LoadBalancer ingress yet", loadBalancerName)
		}

		ingress := service.Status.LoadBalancer.Ingress[0]

		switch {
		case ingress.IP != "":
			ip = ingress.IP
			return nil

		case ingress.Hostname != "":
			ip, err = publicDNSIP(clientset, namespace, ingress.Hostname)
			return err

		default:
			return errors.Errorf("no IP or Hostname for service LoadBalancer %q Ingress", loadBalancerName)
		}
	})

	return ip, err
}

func publicDNSIP(clientset kubernetes.Interface, namespace, fqdn string) (string, error) {
	ips, err := net.LookupIP(fqdn)
	if err != nil {
		return "", errors.Wrapf(err, "error resolving DNS hostname %q for public IP", fqdn)
	}

	return ips[0].String(), nil
}

func firstIPv4InString(body string) (string, error) {
	matches := IPv4RE.FindAllString(body, -1)
	if len(matches) == 0 {
		return "", errors.Errorf("No IPv4 found in: %q", body)
	}

	return matches[0], nil
}
