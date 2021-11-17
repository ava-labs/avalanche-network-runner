# Kubernetes deployment of an avalanche network runner 

## Scope 
The avalanche network runner can operate in 
* local
* kubernetes

environments.

This README is *solely* about the kubernetes environment.

## Overview
Avalanchego is a *stateful* application: It requires a DB and runs with an identity (NodeID).
Standard Kubernetes is best suited for *stateless* applications.

To allow for stateful applications, the **Operator** pattern has been introduced into the kubernetes world [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/).

This means that in order to successfully run an avalanchego network with this network runner tool on kubernetes, a "domain-specific" implementation of the pattern is required.
This implementation can be found at [avalanchego-operator](https://github.com/ava-labs/avalanchego-operator).

There only needs to be running **1 operator instance** per kubernetes namespace.
Therefore it is very important to distinguish between:

* A local kubernetes cluster environment
* A cloud-based cluster environment


## Cloud-based environment

### Github action
If running in a cloud provided (for Ava Labs, typically AWS), then the **devops** team is responsible for setting up the required infrastructure.

**Devops would be deploying the `avalanchego-operator`**.


The normal operation is to create a github action which triggers the deployment to the kubernetes cluster in the cloud. 
An example of such a github action can be found at `examples/github` **TODO**.
The github action triggers the deployment of a self-contained pod or a permissioned `main` with access to the cluster.

In this scenario, devops needs to be informed about a project running such a github action using up kubernetes resources.
They will then provide the necessary environment information (namespaces and other potentially required configurations) to be configured on the github action.
They will also install the appropriate kubernetes self-hosted runner into the repository so that this integration will work.

Usually there would be nothing else to be done for the deployment other than implementing the network logic.


### Without github action
If a deployment without a github action should be required, then devops involvement is likewise required.
They also would deploy the `avalanchego-operator` and provide necessary deployment information (namespace etc.).

The network runner can then analogously be deployed either via self-contained pod or a `main` binary. Both need to have the required permissions.


## Local kubernetes environment
Kubernetes can also be run locally. This is usually only needed for development. In fact, for "normal" avalanchego testing and work, it shouldn't be necessary to run a local kubernetes cluster. However, for developing the integration in this repository, it is highly recommended as it speeds up development time.

To run a local kubernetes environment:


1. Install a kubernetes environment, e.g. `minikube` or `k3s`. We use `k3s` for this README: [k3s](https://k3s.io/). Follow instructions. This will also install `kubectl`.
2. Make sure to run `sudo k3s server --docker --write-kubeconfig-mode 644`. This will allow to use **locally built images** (otherwise you'll need to use a public repo and push there). It may be required to `sudo systemctl stop k3s` service for `k3s` if the installation configured that mode, and disable it: `sudo systemctl disable k3s` (or edit the systemd script). Switch to other terminal.
3. Install `kubectx` to allow to set an easy default environment: [kubectx](https://github.com/ahmetb/kubectx)
4. Set the `$KUBECONFIG` environment variable to read the `k3s` environment: `export KUBECONFIG=/etc/rancher/k3s/k3s.yaml` (put this into your `.profile` or preferred environment persistance method). Make sure this variable is present in all terminals used for this tool.
5. Run `kubectx` to check that the environment is fine (it should print `default` if there are no other kubernetes configs, otherwise, check the `kubectx` docs).
6. **Run the avalanchego-operator locally**
   6a. Clone the repository: `github.com/ava-labs/avalanchego-operator`
   6b. Run `make install` followed by `make run`. This should be everything needed.
7. Install the service account, role and roleconfigs for the network runner pod: `kubectl apply -f examples/k8s/svc-rbac.yaml`.
8. Build this app as a pod: `docker build -f ./examples/k8s/Dockerfile -t k8s-netrunner:alpha .`. If you use a different app name than `k8s-netrunner` and/or tag `alpha`, the `examples/k8s/simple-network-pod.yaml` file needs to be updated accordingly.
9. Deploy the pod to your local cluster via `kubectl apply -f examples/k8s/simple-netrunner-pod.yaml`. This picks the image built in 8. to deploy as a pod. Then from within this pod it starts all other nodes.
10. Check that all is ok with `kubectl get pods`. You will first see the `k8s-netrunner` pod, and after some time, the other configured nodes should appear. To check logs, execute `kubectl logs k8s-netrunner`.
