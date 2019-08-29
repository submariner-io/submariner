## Submariner Operator

Experiential Submariner Operator.

### Generating the Operator

The current (developer-oriented) implementation dynamically generates the
operator. This allows us to consume updates to the underlying best practices of
the Operator SDK. It also results in a clear, working example of how to use the
Operator SDK to create additional operators (perhaps for future parts of
Submariner).

> ./gen_subm_operator.sh

#### Prerequisites

##### Kind

The Operator SDK requires a running K8s cluster. This tooling uses Kind
(Kubernetes in Docker) to deploy that cluster. You'll need to [install Kind][0]
as a prerequisite for generating the Operator. Kind was chosen because it's
currently the tooling used for deploying K8s clusters for Submariner CI.

##### Operator SDK

The SubM Operator generator script uses the Operator SDK tooling to create most
of the Operator from recommended scaffolding. The [Operator SDK must be
installed][1] as a prerequisite of the `gen_subm_operator.sh` script.

### Deploying Submariner using the Operator

After generating the Operator (see docs above), your newly generated operator
is automatically fully integrated into the Submariner CI automation. Simply use
the `deploytool` flag to the standard `make` commands.

> make ci e2e status=keep deploytool=operator

A large set of verifications for the Operator and the resulting Submariner
deployment will automatically run during and after the deployment.

[0]: https://kind.sigs.k8s.io/docs/user/quick-start/
[1]: https://github.com/operator-framework/operator-sdk/blob/master/doc/user/install-operator-sdk.md
