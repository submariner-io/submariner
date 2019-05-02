package util

import (
	"fmt"
	subv1 "github.com/rancher/submariner/pkg/apis/submariner.io/v1"
	"github.com/rancher/submariner/pkg/types"
	"github.com/rdegges/go-ipify"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"
	"log"
	"net"
	"os"
	"strings"
	"syscall"
)

const tokenLength = 64

func getAPIIdentifier(token string) (string, error) {
	if len(token) != tokenLength {
		return "", fmt.Errorf("Token %s length was not %d", token, tokenLength)
	}
	clusterID := token[0:tokenLength/2]
	return clusterID, nil
}

func getConnectSecret(token string) (string, error) {
	if len(token) != tokenLength {
		return "", fmt.Errorf("Token %s length was not %d", token, tokenLength)
	}
	connectSecret := token[tokenLength/2:tokenLength]
	return connectSecret, nil
}

func ParseSecure(token string) (types.Secure, error) {
	if len(token) != tokenLength {
		klog.Fatalf("Token %s length was not %d", token, tokenLength)
	}
	secure := types.Secure{}
	var err error
	secure.SecretKey, err = getConnectSecret(token)
	if err != nil {
		klog.Fatalf("Could not parse token to secret")
	}
	secure.ApiKey, err = getAPIIdentifier(token)
	if err != nil {
		klog.Fatalf("Could not parse token to apikey")
	}
	return secure, nil
}

func GetLocalIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}

func FlattenColors(colorCodes []string) string {
	flattenedColors := colorCodes[0]
	for k, v := range colorCodes {
		if k != 0 {
			flattenedColors = flattenedColors + "," + v
		}
	}
	return flattenedColors
}
func GetLocalCluster(ss types.SubmarinerSpecification) (types.SubmarinerCluster, error) {
	var localCluster types.SubmarinerCluster
	localCluster.ID = ss.ClusterId
	localCluster.Spec.ClusterID = ss.ClusterId
	localCluster.Spec.ClusterCIDR = ss.ClusterCidr
	localCluster.Spec.ServiceCIDR = ss.ServiceCidr
	localCluster.Spec.ColorCodes = ss.ColorCodes
	return localCluster, nil
}
func GetLocalEndpoint(clusterID string, backend string, backendConfig map[string]string, natEnabled bool, subnets []string) (types.SubmarinerEndpoint, error) {
	hostname, err := os.Hostname()
	if err != nil {
		klog.Fatalf("error getting hostname: %s", err.Error())
	}
	privateIP := GetLocalIP()
	endpoint := types.SubmarinerEndpoint{
		Spec: subv1.EndpointSpec{
			CableName: fmt.Sprintf("submariner-cable-%s-%s", clusterID, strings.Replace(privateIP.String(), ".", "-", -1)),
			ClusterID: clusterID,
			Hostname: hostname,
			PrivateIP: privateIP,
			NATEnabled: natEnabled,
			Subnets: subnets,
			Backend: backend,
			BackendConfig: backendConfig,
		},
	}
	if natEnabled {
		publicIP, err := ipify.GetIp()
		if err != nil {
			klog.Fatalf("could not determine public IP: %v", err)
		}
		endpoint.Spec.PublicIP = net.ParseIP(publicIP)
	}
	return endpoint, nil
}

func GetClusterIdFromCableName(cableName string) string {
	// length is 11
	// 0           1    2   3    4    5       6   7  8 9  10
	//submariner-cable-my-super-long_cluster-id-172-16-32-5
	cableSplit := strings.Split(cableName, "-")
	clusterId := cableSplit[2]
	for i := 3; i < len(cableSplit)-4; i++ {
		clusterId = clusterId + "-" + cableSplit[i]
	}
	return clusterId
}

func GetEndpointCRDName(endpoint types.SubmarinerEndpoint) (string, error) {
	return GetEndpointCRDNameFromParams(endpoint.Spec.ClusterID, endpoint.Spec.CableName)
}

func GetEndpointCRDNameFromParams(clusterID, cableName string) (string, error) {
	if clusterID == "" || cableName == "" {
		return "", fmt.Errorf("error, cluster ID or cable name was empty")
	}

	return fmt.Sprintf("%s-%s", clusterID, cableName), nil
}

func GetClusterCRDName(cluster types.SubmarinerCluster) (string, error) {
	return cluster.Spec.ClusterID, nil
}

func CompareEndpointSpec(left, right subv1.EndpointSpec) bool {
	if left.ClusterID == right.ClusterID && left.CableName == right.CableName && left.Hostname == right.Hostname {
		return true
	}
	return false
}

func GetDefaultGatewayInterface() (*net.Interface, error) {
	routes, err := netlink.RouteList(nil, syscall.AF_INET)
	if err != nil {
		return nil, err
	}

	for _, route := range routes {
		if route.Dst == nil || route.Dst.String() == "0.0.0.0/0" {
			if route.LinkIndex == 0 {
				return nil, fmt.Errorf("default gateway interface could not be determined")
			}
			iface, err := net.InterfaceByIndex(route.LinkIndex)
			if err != nil {
				return nil, err
			}
			return iface, nil
		}
	}

	return nil, fmt.Errorf("unable to find default route")
}