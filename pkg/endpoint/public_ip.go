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
	"github.com/submariner-io/admiral/pkg/resource"
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

func parseResolver(resolver string) (string, string, error) {
	method, config, found := strings.Cut(strings.Trim(resolver, " "), ":")
	if !found || method == "" || config == "" {
		return "", "", errors.Errorf("invalid format for %q annotation: %q", v1.GatewayConfigPrefix+v1.PublicIP, resolver)
	}

	return method, config, nil
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
			ip, resolver, err := invokeResolvers(family, k8sClient, submSpec.Namespace, config, func(method string) bool {
				return method == v1.IPv4 || method == v1.IPv6
			})
			if err != nil {
				logger.Errorf(err, "Unable to resolve public IPv%s in an air-gapped deployment using %q - using empty value",
					family, config)
				return "", "", nil
			}

			return ip, resolver, nil
		}

		return invokeResolvers(family, k8sClient, submSpec.Namespace, config, nil)
	case k8snet.IPFamilyUnknown:
	}

	return "", "", nil
}

func invokeResolvers(family k8snet.IPFamily, k8sClient kubernetes.Interface, namespace, config string, useResolver func(string) bool,
) (string, string, error) {
	resolvers := strings.Split(config, ",")

	errs := make([]error, 0, len(resolvers))

	for _, resolver := range resolvers {
		var ip string

		method, param, err := parseResolver(resolver)
		if err == nil {
			if useResolver != nil && !useResolver(method) {
				continue
			}

			ip, err = resolvePublicIP(family, k8sClient, namespace, method, param)
		}

		if err == nil {
			return ip, resolver, nil
		}

		// If this resolver failed, we log it, but we fall back to the next one
		errs = append(errs, errors.Wrapf(err, "\nResolver[%q]", resolver))
	}

	if len(resolvers) > 0 {
		return "", "", errors.Wrapf(k8serrors.NewAggregate(errs),
			"Unable to resolve public IPv%s by any of the resolver methods: %q", family, config)
	}

	return "", "", nil
}

func resolvePublicIP(family k8snet.IPFamily, k8sClient kubernetes.Interface, namespace, method, param string) (string, error) {
	resolverFn, ok := publicIPMethods[method]
	if !ok {
		return "", errors.Errorf("unknown resolver %q in %q annotation", method, v1.GatewayConfigPrefix+v1.PublicIP)
	}

	return resolverFn(family, k8sClient, namespace, param)
}

func publicAPI(family k8snet.IPFamily, _ kubernetes.Interface, _, value string) (string, error) {
	url := value
	if !strings.HasPrefix(url, "http") {
		url = "https://" + value
	}

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
	resolvedIP := ""

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
					resolvedIP = ingress.IP
					return nil
				}
			case ingress.Hostname != "":
				ip, err := publicDNSIP(family, clientset, namespace, ingress.Hostname)
				if err != nil {
					return err
				}

				if ip != "" {
					resolvedIP = ip
					return nil
				}
			}
		}

		return errors.Errorf("no IP or Hostname resolved for service LoadBalancer %q Ingress: %s",
			loadBalancerName, resource.ToJSON(service.Status.LoadBalancer.Ingress))
	})

	return resolvedIP, err //nolint:wrapcheck  // No need to wrap here
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
