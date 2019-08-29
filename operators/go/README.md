## Submariner Operator

Experimental Submariner Operator.

### Generating the Operator

The current (developer-oriented) implementation dynamically generates the
operator. This allows us to consume updates to the underlying best practices of
the Operator SDK. It also results in a clear, working example of how to use the
Operator SDK to create additional operators (perhaps for future parts of
Submariner).

> cd ../../../
> make codegen-operator

That will run the operator sourcecode generation logic in ./gen_subm_operator.sh

### Builiding the operator

> cd ../../..
> make build-operator

### Deploying Submariner using the Operator

After generating the Operator (see docs above), your newly generated operator
is automatically fully integrated into the Submariner CI automation. Simply use
the `deploytool` flag to the standard `make` commands.

> make ci e2e status=keep deploytool=operator

A large set of verifications for the Operator and the resulting Submariner
deployment will automatically run during and after the deployment.
