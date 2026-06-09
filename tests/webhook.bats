#!/usr/bin/env bats

load 'test_helper/bats-support/load'
load 'test_helper/bats-assert/load'

setup_file() {
  export BATS_TEST_TIMEOUT=300
  kubectl apply -f https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/master/doc/crds/daemonset-install.yaml -f https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/master/doc/crds/whereabouts.cni.cncf.io_ippools.yaml -f https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/master/doc/crds/whereabouts.cni.cncf.io_overlappingrangeipreservations.yaml
  
  kubectl -n kube-system wait --for=condition=ready pods -l app=whereabouts --timeout=120s

	# write CNI config to kind nodes
	for node in $(kind get nodes --name dranet-test-cluster); do
		docker cp "$BATS_TEST_DIRNAME"/../tests/manifests/90-dranet-whereabouts.conf "$node":/etc/cni/net.d/90-dranet-whereabouts.conf
	done

	# Build webhook
	(cd "$BATS_TEST_DIRNAME"/../cmd/webhook-whereabouts && go build -o /tmp/webhook-whereabouts .)

	for node in $(kind get nodes --name dranet-test-cluster); do
		docker cp /tmp/webhook-whereabouts "$node":/usr/local/bin/webhook-whereabouts
		docker exec -d "$node" bash -c "nohup /usr/local/bin/webhook-whereabouts --bind-address=127.0.0.1:8080 > /var/log/webhook-whereabouts.log 2>&1 &"
	done

  kubectl patch daemonset dranet -n kube-system --type=json -p='[
    {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--profile-provider=webhook"},
    {"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--webhook-url=http://127.0.0.1:8080"}
  ]'
  kubectl rollout status daemonset/dranet -n kube-system --timeout=120s
  
  # Delete dranet pods to restart them with new config
  kubectl delete pods -n kube-system -l app=dranet
  kubectl wait --for=condition=ready pods --namespace=kube-system -l k8s-app=dranet --timeout=120s
  # Restart dranet pods again to load the new config
  kubectl delete pods -n kube-system -l app=dranet
  kubectl wait --for=condition=ready pods --namespace=kube-system -l k8s-app=dranet --timeout=120s
}

teardown_file() {
  kubectl patch daemonset dranet -n kube-system --type=json -p='[
    {"op": "remove", "path": "/spec/template/spec/containers/0/args/5"},
    {"op": "remove", "path": "/spec/template/spec/containers/0/args/4"}
  ]' || true
  kubectl rollout status daemonset/dranet -n kube-system --timeout=120s
  
  for node in $(kind get nodes --name dranet-test-cluster); do
    docker exec "$node" bash -c "pkill -f webhook-whereabouts || true"
  done
  
  kubectl delete -f https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/master/doc/crds/daemonset-install.yaml || true
}

@test "validate whereabouts cni integration" {
  for node in $(kind get nodes --name dranet-test-cluster); do
    docker exec "$node" bash -c "ip link add dummy0 type dummy || true"
    docker exec "$node" bash -c "ip link set up dev dummy0 || true"
  done

  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/deviceclass.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/whereabouts-claim.yaml
  kubectl apply -f "$BATS_TEST_DIRNAME"/../tests/manifests/pod-whereabouts.yaml

  kubectl wait --for=condition=ready pod/pod-whereabouts --timeout=120s
  
  # Validate pod has IP from whereabouts range
  run kubectl exec pod-whereabouts -- ip -4 addr show
  assert_output --partial "192.168.100."
  
  kubectl delete pod pod-whereabouts
  kubectl delete resourceclaimtemplate whereabouts-claim
  
  for node in $(kind get nodes --name dranet-test-cluster); do
    docker exec "$node" bash -c "ip link delete dev dummy0 || true"
  done
}
