package phpapi

import (
	"context"
"encoding/json"
"fmt"
"github.com/kelseyhightower/envconfig"
"github.com/rancher/submariner/pkg/types"
"github.com/rancher/submariner/pkg/util"
"io/ioutil"
"net/http"
"net/url"
"sync"
"time"
"k8s.io/klog"
)

type PHPAPI struct {
	sync.Mutex
	Proto string
	Server string
	APIToken string
}

type PHPAPISpecification struct {
	Proto string
	Server string
}

func NewPHPAPI(apitoken string) *PHPAPI {
	var pais PHPAPISpecification
	err := envconfig.Process("backend_phpapi", &pais)
	if err != nil {
		klog.Fatal(err)
	}
	klog.Infof("Instantiating PHPAPI Backend at %s://%s with APIToken %s", pais.Proto, pais.Server, apitoken)
	return &PHPAPI{
		Proto: pais.Proto,
		Server: pais.Server,
		APIToken: apitoken,
	}
}

func (p *PHPAPI) GetClusters(colorCodes []string) ([]types.SubmarinerCluster, error) {
	colorCode := util.FlattenColors(colorCodes)
	requestUrl := fmt.Sprintf("%s://%s/clusters.php?plurality=true&identifier=%s&colorcode=%s", p.Proto, p.Server, p.APIToken, colorCode)
	klog.V(8).Infof("request url: %s", requestUrl)
	clustersGetter, err := http.Get(requestUrl)
	if err != nil {
		klog.Errorf("Encountered error while trying to retrieve clusters from the apiserver: %v", err)
		return nil, err
	}
	defer clustersGetter.Body.Close()
	clustersRaw, err := ioutil.ReadAll(clustersGetter.Body)
	var clusters []types.SubmarinerCluster
	klog.V(8).Infof("response body: %v", string(clustersRaw[:]))
	// let's unmarshal into our type the cluster into
	err = json.Unmarshal(clustersRaw, &clusters) // need to actually make the API return json that works for this
	if err != nil {
		klog.Errorf("Error while unmarshaling JSON from clusters")
		return nil, err
	}
	return clusters, nil
}
func (p *PHPAPI) GetCluster(clusterId string) (types.SubmarinerCluster, error) {
	requestUrl := fmt.Sprintf("%s://%s/clusters.php?plurality=false&identifier=%s&cluster_id=%s", p.Proto, p.Server, p.APIToken, clusterId)
	klog.V(8).Infof("request url: %s", requestUrl)
	clustersGetter, err := http.Get(requestUrl)
	if err != nil {
		klog.Errorf("Encountered error while trying to retrieve clusters from the apiserver")
		return types.SubmarinerCluster{}, err
	}
	defer clustersGetter.Body.Close()
	clustersRaw, err := ioutil.ReadAll(clustersGetter.Body)
	klog.V(8).Infof("response body: %v", string(clustersRaw[:]))
	var cluster types.SubmarinerCluster
	json.Unmarshal(clustersRaw, &cluster)

	return cluster, nil
}
func (p *PHPAPI) GetEndpoints(clusterId string) ([]types.SubmarinerEndpoint, error) {
	requestUrl := fmt.Sprintf("%s://%s/endpoints.php?plurality=true&identifier=%s&cluster_id=%s", p.Proto, p.Server, p.APIToken, clusterId)
	endpointsGetter, err := http.Get(requestUrl)
	if err != nil {
		klog.Errorf("encountered error while trying to retrieve endpoints from the apiserver: %v", err)
		return nil, err
	}
	defer endpointsGetter.Body.Close()
	endpointsRaw, err := ioutil.ReadAll(endpointsGetter.Body)
	klog.V(8).Infof("response body: %v", string(endpointsRaw[:]))
	var endpoints []types.SubmarinerEndpoint
	err = json.Unmarshal(endpointsRaw, &endpoints)
	if err != nil {
		klog.Errorf("error while unmarshaling JSON from endpoints")
		return nil, err
	}
	return endpoints, nil
}
func (p *PHPAPI) GetEndpoint(clusterId string, cableName string) (types.SubmarinerEndpoint, error) {
	klog.Errorf("GetEndpoint not implemented")
	return types.SubmarinerEndpoint{}, nil
}
func (p *PHPAPI) WatchClusters(ctx context.Context, selfClusterId string, colorCodes []string, onChange func(cluster types.SubmarinerCluster, deleted bool) error) error {
	colorCode := util.FlattenColors(colorCodes)
	klog.Infof("Starting watch for colorCode %s", colorCode)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			clusters, err := p.GetClusters(colorCodes)
			if err != nil {
				klog.Fatal(err)
			}
			klog.Infof("Got clusters from API")

			for _, cluster := range clusters {
				if selfClusterId != cluster.ID {
					onChange(cluster, false)
				}
			}

			klog.Infof("Sleeping 5 seconds")
			time.Sleep(5 * time.Second)
		}
	}()
	wg.Wait()
	klog.Errorf("I shouldn't have exited")
	return nil
}
func (p *PHPAPI) WatchEndpoints(ctx context.Context, selfClusterId string, colorCodes []string, onChange func(endpoint types.SubmarinerEndpoint, deleted bool) error) error {

	colorCode := util.FlattenColors(colorCodes)
	klog.Infof("Starting PHPAPI endpoint watch for colorCode %s", colorCode)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			clusters, err := p.GetClusters(colorCodes)
			if err != nil {
				klog.Fatal(err)
			}
			klog.Infof("Got clusters from API: %v", clusters)
			for _, cluster := range clusters {
				endpoints, err := p.GetEndpoints(cluster.ID)
				klog.Infof("Got endpoints from API: %v", endpoints)
				if err != nil {
					klog.Fatal(err)
				}
				for _, endpoint := range endpoints {
					if selfClusterId != endpoint.Spec.ClusterID {
						onChange(endpoint, false)
					}
				}
			}

			klog.Infof("Sleeping 5 seconds")
			time.Sleep(5 * time.Second)
		}
	}()
	wg.Wait()
	klog.Errorf("I shouldn't have exited")
	return nil
}
func (p *PHPAPI) SetCluster(cluster types.SubmarinerCluster) error {
	marshaledCluster, err := json.Marshal(cluster)
	klog.Infof("Setting cluster %s", string(marshaledCluster))
	if err != nil {
		return err
	}
	formVal := url.Values{}
	formVal.Set("action", "reconcile")
	formVal.Add("cluster",string(marshaledCluster))
	poster, err := http.PostForm(fmt.Sprintf("%s://%s/clusters.php?identifier=%s", p.Proto, p.Server, p.APIToken), formVal)
	if err != nil {
		klog.Errorf("encountered error setting cluster")
		klog.Infof("marshalled cluster was %s", string(marshaledCluster))
		return err
	}
	defer poster.Body.Close()
	return nil
}
func (p *PHPAPI) SetEndpoint(local types.SubmarinerEndpoint) error {
	marshaledEndpoint, err := json.Marshal(local)
	if err != nil {
		return err
	}
	formVal := url.Values{}
	formVal.Set("action", "reconcile")
	formVal.Add("endpoint", string(marshaledEndpoint))
	formedURL := fmt.Sprintf("%s://%s/endpoints.php?identifier=%s&cluster_id=%s", p.Proto, p.Server, p.APIToken, local.Spec.ClusterID)
	klog.V(8).Infof("Formed URL was %s", formedURL)
	poster, err := http.PostForm(formedURL, formVal)
	if err != nil {
		klog.Errorf("Error while setting endpoint %v", err)
		return err
	}
	defer poster.Body.Close()

	return nil
}

func (p *PHPAPI) RemoveEndpoint(clusterId, cableName string) error {
	formVal := url.Values{}
	formVal.Set("action", "delete")
	formVal.Add("cable_name", cableName)
	formedURL := fmt.Sprintf("%s://%s/endpoints.php?identifier=%s&cluster_id=%s", p.Proto, p.Server, p.APIToken, clusterId)
	klog.V(8).Infof("Formed URL was %s", formedURL)
	poster, err := http.PostForm(formedURL, formVal)
	if err != nil {
		klog.Errorf("Error while removing endpoint %v", err)
		return err
	}
	defer poster.Body.Close()
	return nil
}
func (p *PHPAPI) RemoveCluster(clusterId string) error {
	return nil
}
