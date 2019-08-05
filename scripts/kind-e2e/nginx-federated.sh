#!/usr/bin/env bash
set -e

TEST_NS="test-ns"
export KUBECONFIG=$(echo $(git rev-parse --show-toplevel)/output/kind-config/local-dev/kind-config-cluster{1..3} | sed 's/ /:/g')
kubectl config use-context cluster1

echo "Creating a test namespace ${TEST_NS}."
kubectl create ns ${TEST_NS}

echo "Creating a nginx application resources in namespace ${TEST_NS}."
kubectl apply -n ${TEST_NS} -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: federated-web-file
data:
  index.html: "Hello from Kubernetes Cluster Federation!"
EOF

kubectl apply -n ${TEST_NS} -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: federated-nginx
  labels:
    app: federated-nginx
spec:
  replicas: 1
  selector:
    matchLabels:
      app: federated-nginx
  template:
    metadata:
      labels:
        app: federated-nginx
    spec:
      containers:
      - image: nginx:alpine
        name: federated-nginx
        volumeMounts:
        - name: federated-web-file
          mountPath: /usr/share/nginx/html/
      volumes:
        - name: federated-web-file
          configMap:
            name: federated-web-file
EOF

kubectl apply -n ${TEST_NS} -f - <<EOF
apiVersion: v1
kind: Service
metadata:
  name: federated-nginx
  labels:
    app: federated-nginx
spec:
  ports:
  - nodePort: 30080
    port: 80
    protocol: TCP
    targetPort: 80
  selector:
    app: federated-nginx
  type: NodePort
EOF

echo "Federating resources in namespace ${TEST_NS} to member clusters."
kubefedctl federate ns ${TEST_NS} --contents --skip-api-resources 'pods,secrets,serviceaccount,replicaset'

for i in 1 2 3; do
    IP=$(kubectl get node -o jsonpath="{.items[0].status.addresses[0].address}" --context cluster${i})
    PORT=$(kubectl get service -n ${TEST_NS} --context cluster${i} -o jsonpath="{.items[0].spec.ports[0].nodePort}")
    echo "cluster${i}: $(curl -s ${IP}:${PORT})"
done

echo "Changing index.html in federated configmap."
kubectl patch federatedconfigmap federated-web-file -n ${TEST_NS} --type=merge -p '{"spec": {"template": {"data": {"index.html": "Hello from federated service!"}}}}'

# Wait for a minute or so for the changes to be replicated to all federated clusters.
for i in 1 2 3; do
    IP=$(kubectl get node -o jsonpath="{.items[0].status.addresses[0].address}" --context cluster${i})
    PORT=$(kubectl get service -n ${TEST_NS} --context cluster${i} -o jsonpath="{.items[0].spec.ports[0].nodePort}")
    echo "cluster${i}: $(curl -s ${IP}:${PORT})"
done

echo "Updating override of federated deployment nginx to increase 'replicas' to 2 in cluster2."
kubectl patch federateddeployment federated-nginx -n ${TEST_NS} --type=merge --patch \
    '{"spec" : {"overrides": [{"clusterName" : "cluster2", "clusterOverrides": [{"path": "spec.replicas", "value" : 2}]}]}}'


echo "Updating placement to include only 'cluster2' so that the deployment will be removed from 'cluster1/3'"
kubectl patch federateddeployment federated-nginx -n ${TEST_NS} --type=merge --patch '{"spec": {"placement": {"clusters": [{"name": "cluster2"}]}}}'