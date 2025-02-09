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
	"bytes"
	"context"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8serrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	k8snet "k8s.io/utils/net"
)

type publicIPResolverFunction func(family k8snet.IPFamily, clientset kubernetes.Interface, namespace, value string) (string, error)

var publicIPMethods = map[string]publicIPResolverFunction{
	v1.API:          publicAPI,
	v1.IPv4:         publicIP,
	v1.IPv6:         publicIP,
	v1.LoadBalancer: publicLoadBalancerIP,
	v1.DNS:          publicDNSIP,
}

var (
	IPv4RE = regexp.MustCompile(`(?:\d{1,3}\.){3}\d{1,3}`)
	IPv6RE = regexp.MustCompile(
		`(?i)(?:[a-f0-9]{1,4}:){7}[a-f0-9]{1,4}|` +
			`(?:[a-f0-9]{1,4}:){1,6}:([a-f0-9]{1,4})?|` +
			`(?:[a-f0-9]{1,4}:){1,5}(?::[a-f0-9]{1,4}){1,2}|` +
			`(?:[a-f0-9]{1,4}:){1,4}(?::[a-f0-9]{1,4}){1,3}|` +
			`(?:[a-f0-9]{1,4}:){1,3}(?::[a-f0-9]{1,4}){1,4}|` +
			`(?:[a-f0-9]{1,4}:){1,2}(?::[a-f0-9]{1,4}){1,5}|` +
			`[a-f0-9]{1,4}(?::[a-f0-9]{1,4}){1,6}|` +
			`::(?:[a-f0-9]{1,4}:){1,7}|` +
			`::(?:[a-f0-9]{1,4}){1,7}|` +
			`fe80:(?::[a-f0-9]{0,4}){0,4}%[0-9a-zA-Z]+|` +
			`::(ffff(?::0{1,4}){0,1}:){0,1}(25[0-5]|2[0-4][0-9]|[0-1]?[0-9][0-9]?\.){3,3}` +
			`(25[0-5]|2[0-4][0-9]|[0-1]?[0-9][0-9]?)|` +
			`(?:[a-f0-9]{1,4}:){1,4}:(25[0-5]|2[0-4][0-9]|[0-1]?[0-9][0-9]?\.){3,3}` +
			`(25[0-5]|2[0-4][0-9]|[0-1]?[0-9][0-9]?)|` +
			`(?:[a-f0-9]{1,4}:){1,7}:?[a-f0-9]{1,4}`,
	)
)

func getPublicIPResolvers(family k8snet.IPFamily) string {
	var serverList []string

	switch family {
	case k8snet.IPv4:
		serverList = []string{
			"api:ip4.seeip.org", "api:ipecho.net/plain", "api:ifconfig.me",
			"api:ipinfo.io/ip", "api:4.ident.me", "api:checkip.amazonaws.com", "api:4.icanhazip.com",
			"api:myexternalip.com/raw", "api:4.tnedi.me", "api:api.ipify.org",
		}
	case k8snet.IPv6:
		serverList = []string{
			"api:api64.ipify.org", "api:api6.ipify.org",
		}
	case k8snet.IPFamilyUnknown:
	}

	rand.Shuffle(len(serverList), func(i, j int) { serverList[i], serverList[j] = serverList[j], serverList[i] })

	return strings.Join(serverList, ",")
}

func getPublicIP(family k8snet.IPFamily, submSpec *types.SubmarinerSpecification, k8sClient kubernetes.Interface,
	backendConfig map[string]string, airGapped bool,
) (string, string, error) {
	switch family {
	case k8snet.IPv4, k8snet.IPv6:
		// If the node is annotated with a public-ip, the same is used as the public-ip of local endpoint.
		config, ok := backendConfig[v1.PublicIP]
		if !ok {
			if submSpec.PublicIP != "" {
				config = submSpec.PublicIP
			} else {
				config = getPublicIPResolvers(family)
			}
		}

		if airGapped {
			ip, resolver, err := resolveIPInAirGappedDeployment(family, k8sClient, submSpec.Namespace, config)
			if err != nil {
				logger.Errorf(err, "Error resolving public IP%s in an air-gapped deployment, using empty value: %s", family, config)
				return "", "", nil
			}

			return ip, resolver, nil
		}

		resolvers := strings.Split(config, ",")

		errs := make([]error, 0, len(resolvers))

		for _, resolver := range resolvers {
			resolver = strings.Trim(resolver, " ")

			parts := strings.Split(resolver, ":")

			// for IPv6 format is ipv6=FD00::BE2:54:34:2
			if len(parts) != 2 && family == k8snet.IPv6 {
				parts = strings.Split(resolver, "=")
			}

			if len(parts) != 2 {
				return "", "", errors.Errorf("invalid format for %q annotation: %q", v1.GatewayConfigPrefix+v1.PublicIP, config)
			}

			ip, err := resolvePublicIP(family, k8sClient, submSpec.Namespace, parts)
			if err == nil {
				return ip, resolver, nil
			}

			// If this resolver failed, we log it, but we fall back to the next one
			errs = append(errs, errors.Wrapf(err, "\nResolver[%q]", resolver))
		}

		if len(resolvers) > 0 {
			return "", "", errors.Wrapf(k8serrors.NewAggregate(errs), "Unable to resolve public IP by any of the resolver methods")
		}

	case k8snet.IPFamilyUnknown:
	}

	return "", "", nil
}

func resolveIPInAirGappedDeployment(
	family k8snet.IPFamily, k8sClient kubernetes.Interface, namespace, config string,
) (string, string, error) {
	resolvers := strings.Split(config, ",")

	for _, resolver := range resolvers {
		resolver = strings.Trim(resolver, " ")

		parts := strings.Split(resolver, ":")
		// for IPv6 format is ipv6=FD00::BE2:54:34:2
		if len(parts) != 2 && family == k8snet.IPv6 {
			parts = strings.Split(resolver, "=")
		}

		if len(parts) != 2 {
			return "", "", errors.Errorf("invalid format for %q annotation: %q", v1.GatewayConfigPrefix+v1.PublicIP, config)
		}

		if parts[0] != v1.IPv4 && parts[0] != v1.IPv6 {
			continue
		}

		ip, err := resolvePublicIP(family, k8sClient, namespace, parts)

		return ip, resolver, err
	}

	return "", "", nil
}

func resolvePublicIP(family k8snet.IPFamily, k8sClient kubernetes.Interface, namespace string, parts []string) (string, error) {
	method, ok := publicIPMethods[parts[0]]
	if !ok {
		return "", errors.Errorf("unknown resolver %q in %q annotation", parts[0], v1.GatewayConfigPrefix+v1.PublicIP)
	}

	return method(family, k8sClient, namespace, parts[1])
}

func publicAPI(family k8snet.IPFamily, _ kubernetes.Interface, _, value string) (string, error) {
	url := "https://" + value

	httpClient := http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}

	response, err := httpClient.Get(url)
	if err != nil {
		return "", errors.Wrapf(err, "retrieving public IP from %s", url)
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", errors.Wrapf(err, "reading API response from %s", url)
	}

	return firstIPInString(family, string(body))
}

func publicIP(family k8snet.IPFamily, _ kubernetes.Interface, _, value string) (string, error) {
	return firstIPInString(family, value)
}

var loadBalancerRetryConfig = wait.Backoff{
	Cap:      6 * time.Minute,
	Duration: 5 * time.Second,
	Factor:   1.2,
	Steps:    24,
}

func publicLoadBalancerIP(family k8snet.IPFamily, clientset kubernetes.Interface, namespace, loadBalancerName string) (string, error) {
	ip := ""

	err := retry.OnError(loadBalancerRetryConfig, func(err error) bool {
		logger.Infof("Waiting for LoadBalancer to be ready: %s", err)
		return true
	}, func() error {
		service, err := clientset.CoreV1().Services(namespace).Get(context.TODO(), loadBalancerName, metav1.GetOptions{})
		if err != nil {
			return errors.Wrapf(err, "error getting service %q for the public IP address", loadBalancerName)
		}

		if len(service.Status.LoadBalancer.Ingress) < 1 {
			return errors.Errorf("service %q doesn't contain any LoadBalancer ingress yet", loadBalancerName)
		}

		for _, ingress := range service.Status.LoadBalancer.Ingress {
			switch {
			case ingress.IP != "":
				if k8snet.IPFamilyOfString(ingress.IP) == family {
					ip = ingress.IP
					return nil
				}
			case ingress.Hostname != "":
				ip, err = publicDNSIP(family, clientset, namespace, ingress.Hostname)
				return err
			}
		}

		return errors.Errorf("no IP or Hostname for service LoadBalancer %q Ingress", loadBalancerName)
	})

	return ip, err //nolint:wrapcheck  // No need to wrap here
}

func publicDNSIP(family k8snet.IPFamily, _ kubernetes.Interface, _, fqdn string) (string, error) {
	ips, err := net.LookupIP(fqdn)
	if err != nil {
		return "", errors.Wrapf(err, "error resolving DNS hostname %q for public IP", fqdn)
	}

	var filteredIPs []net.IP

	for _, ip := range ips {
		if k8snet.IPFamilyOf(ip) == family {
			filteredIPs = append(filteredIPs, ip)
		}
	}

	if len(filteredIPs) > 1 {
		sort.Slice(filteredIPs, func(i, j int) bool {
			return bytes.Compare(filteredIPs[i], filteredIPs[j]) < 0
		})
	}

	return filteredIPs[0].String(), nil
}

func firstIPInString(family k8snet.IPFamily, body string) (string, error) {
	var matches []string

	if family == k8snet.IPv4 {
		matches = IPv4RE.FindAllString(body, -1)
	} else {
		matches = IPv6RE.FindAllString(body, -1)
	}

	if len(matches) == 0 {
		return "", errors.Errorf("No IPv%s found in: %q", family, body)
	}

	return matches[0], nil
}
