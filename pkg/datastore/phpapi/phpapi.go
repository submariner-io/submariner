package phpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/submariner-io/submariner/pkg/datastore"
	"github.com/submariner-io/submariner/pkg/log"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog"
)

type PHPAPI struct {
	sync.Mutex
	Proto    string
	Server   string
	APIToken string
}

type Specification struct {
	Proto  string
	Server string
}

func NewPHPAPI(apitoken string) (datastore.Datastore, error) {
	var pais Specification
	err := envconfig.Process("backend_phpapi", &pais)
	if err != nil {
		return nil, fmt.Errorf("error processing environment config for backend_phpapi: %v", err)
	}

	klog.Infof("Instantiating PHPAPI Backend at %s://%s with APIToken %s", pais.Proto, pais.Server, apitoken)
	return &PHPAPI{
		Proto:    pais.Proto,
		Server:   pais.Server,
		APIToken: apitoken,
	}, nil
}

func (p *PHPAPI) GetClusters(colorCodes []string) ([]types.SubmarinerCluster, error) {
	colorCode := util.FlattenColors(colorCodes)
	requestURL := fmt.Sprintf("%s://%s/clusters.php?plurality=true&identifier=%s&colorcode=%s", p.Proto, p.Server, p.APIToken, colorCode)
	klog.V(log.DEBUG).Infof("GetClusters: request url: %s", requestURL)
	clustersGetter, err := http.Get(requestURL)
	if err != nil {
		return nil, fmt.Errorf("error retrieving clusters from %s: %v", requestURL, err)
	}

	defer clustersGetter.Body.Close()
	clustersRaw, err := ioutil.ReadAll(clustersGetter.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response from %s: %v", requestURL, err)
	}

	klog.V(log.DEBUG).Infof("GetClusters: response body: %v", string(clustersRaw[:]))

	// let's unmarshal into our type the cluster into
	// need to actually make the API return json that works for this
	var clusters []types.SubmarinerCluster
	if err = json.Unmarshal(clustersRaw, &clusters); err != nil {
		return nil, fmt.Errorf("error unmarshalling JSON %s: %v", string(clustersRaw[:]), err)
	}
	return clusters, nil
}

func (p *PHPAPI) GetCluster(clusterID string) (*types.SubmarinerCluster, error) {
	requestURL := fmt.Sprintf("%s://%s/clusters.php?plurality=false&identifier=%s&cluster_id=%s", p.Proto, p.Server, p.APIToken, clusterID)
	klog.V(log.DEBUG).Infof("GetCluster: request url: %s", requestURL)
	clustersGetter, err := http.Get(requestURL)
	if err != nil {
		return nil, fmt.Errorf("error retrieving clusters from %s: %v", requestURL, err)
	}

	defer clustersGetter.Body.Close()
	clustersRaw, err := ioutil.ReadAll(clustersGetter.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response from %s: %v", requestURL, err)
	}

	klog.V(log.DEBUG).Infof("GetCluster: response body: %v", string(clustersRaw[:]))

	var cluster types.SubmarinerCluster
	if err = json.Unmarshal(clustersRaw, &cluster); err != nil {
		return nil, fmt.Errorf("error unmarshalling JSON %s: %v", string(clustersRaw[:]), err)
	}

	return &cluster, nil
}

func (p *PHPAPI) GetEndpoints(clusterID string) ([]types.SubmarinerEndpoint, error) {
	requestURL := fmt.Sprintf("%s://%s/endpoints.php?plurality=true&identifier=%s&cluster_id=%s", p.Proto, p.Server, p.APIToken, clusterID)
	endpointsGetter, err := http.Get(requestURL)
	if err != nil {
		return nil, fmt.Errorf("error retrieving endpoints from %s: %v", requestURL, err)
	}

	defer endpointsGetter.Body.Close()
	endpointsRaw, err := ioutil.ReadAll(endpointsGetter.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response from %s: %v", requestURL, err)
	}

	klog.V(log.DEBUG).Infof("GetEndpoints: response body: %v", string(endpointsRaw[:]))

	var endpoints []types.SubmarinerEndpoint
	if err = json.Unmarshal(endpointsRaw, &endpoints); err != nil {
		return nil, fmt.Errorf("error unmarshalling JSON %s: %v", string(endpointsRaw[:]), err)
	}
	return endpoints, nil
}

func (p *PHPAPI) WatchClusters(ctx context.Context, selfClusterID string, colorCodes []string, onChange datastore.OnClusterChange) error {
	colorCode := util.FlattenColors(colorCodes)
	klog.Infof("Starting watch for colorCode %s", colorCode)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			clusters, err := p.GetClusters(colorCodes)
			if err != nil {
				utilruntime.HandleError(err)
			} else {
				klog.V(log.DEBUG).Infof("Got clusters from API: %#v", clusters)
				for _, cluster := range clusters {
					if selfClusterID != cluster.ID {
						utilruntime.HandleError(onChange(&cluster, false))
					}
				}
			}

			klog.V(log.TRACE).Infof("Sleeping 5 seconds")
			time.Sleep(5 * time.Second)
		}
	}()
	wg.Wait()
	klog.Errorf("I shouldn't have exited")
	return nil
}

func (p *PHPAPI) WatchEndpoints(ctx context.Context, selfClusterID string, colorCodes []string, onChange datastore.OnEndpointChange) error {
	colorCode := util.FlattenColors(colorCodes)
	klog.Infof("Starting PHPAPI endpoint watch for colorCode %s", colorCode)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			clusters, err := p.GetClusters(colorCodes)
			if err != nil {
				utilruntime.HandleError(err)
			} else {
				klog.V(log.DEBUG).Infof("Got clusters from API: %#v", clusters)
				for _, cluster := range clusters {
					endpoints, err := p.GetEndpoints(cluster.ID)
					if err != nil {
						utilruntime.HandleError(err)
						continue
					}

					klog.V(log.DEBUG).Infof("Got endpoints from API: %#v", endpoints)
					for _, endpoint := range endpoints {
						if selfClusterID != endpoint.Spec.ClusterID {
							utilruntime.HandleError(onChange(&endpoint, false))
						}
					}
				}
			}

			klog.V(log.TRACE).Infof("Sleeping 5 seconds")
			time.Sleep(5 * time.Second)
		}
	}()
	wg.Wait()
	klog.Errorf("I shouldn't have exited")
	return nil
}

func (p *PHPAPI) SetCluster(cluster *types.SubmarinerCluster) error {
	marshaledCluster, err := json.Marshal(cluster)
	if err != nil {
		return fmt.Errorf("error marshalling %#v: %v", cluster, err)
	}

	formVal := url.Values{}
	formVal.Set("action", "reconcile")
	formVal.Add("cluster", string(marshaledCluster))
	requestURL := fmt.Sprintf("%s://%s/clusters.php?identifier=%s", p.Proto, p.Server, p.APIToken)

	klog.V(log.DEBUG).Infof("Setting cluster %s via URL %s", string(marshaledCluster), requestURL)
	poster, err := http.PostForm(requestURL, formVal)
	if err != nil {
		return fmt.Errorf("error setting cluster %s via URL %s: %v", string(marshaledCluster), requestURL, err)
	}
	defer poster.Body.Close()
	return nil
}

func (p *PHPAPI) SetEndpoint(endpoint *types.SubmarinerEndpoint) error {
	marshaledEndpoint, err := json.Marshal(endpoint)
	if err != nil {
		return fmt.Errorf("error marshalling %#v: %v", endpoint, err)
	}

	formVal := url.Values{}
	formVal.Set("action", "reconcile")
	formVal.Add("endpoint", string(marshaledEndpoint))
	requestURL := fmt.Sprintf("%s://%s/endpoints.php?identifier=%s&cluster_id=%s", p.Proto, p.Server, p.APIToken,
		endpoint.Spec.ClusterID)

	klog.V(log.DEBUG).Infof("Setting endpoint %s via URL %s", string(marshaledEndpoint), requestURL)
	poster, err := http.PostForm(requestURL, formVal)
	if err != nil {
		return fmt.Errorf("error setting endpoint %s via URL %s: %v", string(marshaledEndpoint), requestURL, err)
	}
	defer poster.Body.Close()

	return nil
}

func (p *PHPAPI) RemoveEndpoint(clusterID, cableName string) error {
	formVal := url.Values{}
	formVal.Set("action", "delete")
	formVal.Add("cable_name", cableName)
	requestURL := fmt.Sprintf("%s://%s/endpoints.php?identifier=%s&cluster_id=%s", p.Proto, p.Server, p.APIToken, clusterID)

	klog.V(log.DEBUG).Infof("Removing endpoint %s via URL %s", cableName, requestURL)
	poster, err := http.PostForm(requestURL, formVal)
	if err != nil {
		return fmt.Errorf("error removing endpoint %s via URL %s: %v", cableName, requestURL, err)
	}
	defer poster.Body.Close()
	return nil
}
