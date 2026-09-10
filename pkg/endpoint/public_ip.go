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
	goerrors "errors"
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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	k8snet "k8s.io/utils/net"
)

type publicIPResolverFunction func(ctx context.Context, family k8snet.IPFamily, clientset kubernetes.Interface, namespace, value string,
) (string, error)

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

func GetPublicIP(ctx context.Context, family k8snet.IPFamily, submSpec *types.SubmarinerSpecification, k8sClient kubernetes.Interface,
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
			ip, resolver, err := resolvePublicIPAirGapped(ctx, family, k8sClient, submSpec.Namespace, config)
			if err != nil {
				logger.Errorf(err, "Unable to resolve public IPv%s in an air-gapped deployment using %q - using empty value",
					family, config)
			}

			return ip, resolver, nil
		}

		return invokeResolvers(ctx, family, k8sClient, submSpec.Namespace, config, nil)
	case k8snet.IPFamilyUnknown:
	}

	return "", "", nil
}

func resolvePublicIPAirGapped(ctx context.Context, family k8snet.IPFamily, k8sClient kubernetes.Interface, namespace, config string,
) (string, string, error) {
	var errs []error

	ip, resolver, err := invokeResolvers(ctx, family, k8sClient, namespace, config, func(method string) bool {
		return method == v1.IPv4 || method == v1.IPv6
	})
	if ip != "" {
		return ip, resolver, nil
	}

	if err != nil {
		errs = append(errs, err)
	}

	for r := range strings.SplitSeq(config, ",") {
		method, param, parseErr := parseResolver(r)
		if parseErr != nil {
			errs = append(errs, parseErr)
			continue
		}

		if method != v1.LoadBalancer {
			continue
		}

		ip, lbErr := publicLoadBalancerDirectIP(ctx, family, k8sClient, namespace, param)
		if lbErr == nil && ip != "" {
			return ip, r, nil
		}

		if lbErr != nil {
			errs = append(errs, errors.Wrapf(lbErr, "\nResolver[%q]", r))
		}
	}

	return "", "", goerrors.Join(errs...)
}

func invokeResolvers(ctx context.Context,
	family k8snet.IPFamily, k8sClient kubernetes.Interface, namespace, config string,
	useResolver func(string) bool,
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

			ip, err = resolvePublicIP(ctx, family, k8sClient, namespace, method, param)
		}

		// If the context has been cancelled, log it and bail out
		if ctx.Err() != nil {
			errs = append(errs, errors.Wrapf(ctx.Err(), "\nResolver[%q]", resolver))
			break
		}

		if err == nil {
			return ip, resolver, nil
		}

		// If this resolver failed, we log it, but we fall back to the next one
		errs = append(errs, errors.Wrapf(err, "\nResolver[%q]", resolver))
	}

	if len(resolvers) > 0 {
		return "", "", errors.Wrapf(goerrors.Join(errs...),
			"Unable to resolve public IPv%s by any of the resolver methods: %q", family, config)
	}

	return "", "", nil
}

func resolvePublicIP(ctx context.Context, family k8snet.IPFamily, k8sClient kubernetes.Interface, namespace, method, param string,
) (string, error) {
	resolverFn, ok := publicIPMethods[method]
	if !ok {
		return "", errors.Errorf("unknown resolver %q in %q annotation", method, v1.GatewayConfigPrefix+v1.PublicIP)
	}

	return resolverFn(ctx, family, k8sClient, namespace, param)
}

func publicAPI(ctx context.Context, family k8snet.IPFamily, _ kubernetes.Interface, _, value string) (string, error) {
	url := value
	if !strings.HasPrefix(url, "http") {
		url = "https://" + value
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var response *http.Response

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err == nil {
		response, err = http.DefaultClient.Do(req)
	}

	if err != nil {
		return "", errors.Wrapf(err, "error retrieving public IP from %s", url)
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", errors.Wrapf(err, "error reading API response from %s", url)
	}

	return firstIPInString(family, string(body))
}

func publicIP(_ context.Context, family k8snet.IPFamily, _ kubernetes.Interface, _, value string) (string, error) {
	return firstIPInString(family, value)
}

var LoadBalancerRetryConfig = wait.Backoff{
	Cap:      6 * time.Minute,
	Duration: 5 * time.Second,
	Factor:   1.2,
	Steps:    24,
}

func publicLoadBalancerIP(ctx context.Context, family k8snet.IPFamily, clientset kubernetes.Interface, namespace, loadBalancerName string,
) (string, error) {
	return resolveLoadBalancerIP(ctx, family, clientset, namespace, loadBalancerName, publicDNSIP)
}

// publicLoadBalancerDirectIP resolves the public IP from a LoadBalancer service using only direct
// IP entries in the ingress status. Hostname entries are skipped, making this safe for air-gapped
// deployments where DNS may not be available.
func publicLoadBalancerDirectIP(ctx context.Context, family k8snet.IPFamily, clientset kubernetes.Interface,
	namespace, loadBalancerName string,
) (string, error) {
	return resolveLoadBalancerIP(ctx, family, clientset, namespace, loadBalancerName, nil)
}

func resolveLoadBalancerIP(ctx context.Context, family k8snet.IPFamily, clientset kubernetes.Interface, namespace, loadBalancerName string,
	hostnameResolver publicIPResolverFunction,
) (string, error) {
	resolvedIP := ""
	var lastErr error

	err := wait.ExponentialBackoffWithContext(ctx, LoadBalancerRetryConfig, func(ctx context.Context) (bool, error) {
		service, err := clientset.CoreV1().Services(namespace).Get(ctx, loadBalancerName, metav1.GetOptions{})
		if err != nil {
			lastErr = errors.Wrapf(err, "error getting service %q for the public IP address", loadBalancerName)
			return false, nil
		}

		if len(service.Status.LoadBalancer.Ingress) < 1 {
			lastErr = errors.Errorf("service %q doesn't contain any LoadBalancer ingress yet", loadBalancerName)
			return false, nil
		}

		ip, done, resolveErr := resolveIngressIP(ctx, family, clientset, namespace, loadBalancerName,
			service.Status.LoadBalancer.Ingress, hostnameResolver)
		if resolveErr != nil {
			lastErr = resolveErr
		}

		resolvedIP = ip

		return done, nil
	})
	if wait.Interrupted(err) {
		if lastErr != nil {
			return resolvedIP, lastErr
		}
	}

	return resolvedIP, errors.Wrapf(err, "error resolving service LoadBalancer %q", loadBalancerName)
}

func resolveIngressIP(ctx context.Context, family k8snet.IPFamily, clientset kubernetes.Interface, namespace, loadBalancerName string,
	ingresses []corev1.LoadBalancerIngress, hostnameResolver publicIPResolverFunction,
) (string, bool, error) {
	hasAnyDirectIP := false

	for _, ingress := range ingresses {
		if ingress.IP != "" {
			hasAnyDirectIP = true

			if k8snet.IPFamilyOfString(ingress.IP) == family {
				return ingress.IP, true, nil
			}
		}
	}

	if hostnameResolver == nil {
		if !hasAnyDirectIP {
			for _, ingress := range ingresses {
				if ingress.Hostname != "" {
					logger.Warningf("Skipping hostname ingress %q for service %q: DNS resolution is not available "+
						"in air-gapped mode; only LoadBalancer services with a direct IP in the ingress status are supported",
						ingress.Hostname, loadBalancerName)
				}
			}

			return "", true, nil
		}
	} else {
		for _, ingress := range ingresses {
			if ingress.Hostname == "" {
				continue
			}

			ip, err := hostnameResolver(ctx, family, clientset, namespace, ingress.Hostname)
			if err != nil {
				return "", false, err
			}

			if ip != "" {
				return ip, true, nil
			}
		}
	}

	return "", false, errors.Errorf("no IP or Hostname resolved for service LoadBalancer %q Ingress: %s",
		loadBalancerName, resource.ToJSON(ingresses))
}

var LookupIP = net.DefaultResolver.LookupIP

func publicDNSIP(ctx context.Context, family k8snet.IPFamily, _ kubernetes.Interface, _, fqdn string) (string, error) {
	ips, err := LookupIP(ctx, "ip"+string(family), fqdn)
	if err != nil {
		return "", errors.Wrapf(err, "error resolving DNS hostname %q for public IP", fqdn)
	}

	var filteredIPs []net.IP

	for _, ip := range ips {
		if k8snet.IPFamilyOf(ip) == family {
			filteredIPs = append(filteredIPs, ip)
		}
	}

	if len(filteredIPs) == 0 {
		return "", nil
	}

	sort.Slice(filteredIPs, func(i, j int) bool {
		return bytes.Compare(filteredIPs[i], filteredIPs[j]) < 0
	})

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
		return "", errors.Errorf("no IPv%s address found in: %q", family, body)
	}

	return matches[0], nil
}
