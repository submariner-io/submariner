# Proposal for release management in the submariner-io project

## Introduction

Releasing submariner consists of several manual [steps](https://submariner-io.github.io/contributing/release-process/)
that need to be carefully executed in order across multiple projects.

This process can be automated as described in the following sections.

## What's a release

   A submariner release is the sum of all components, tagged on a specific set of versions,
   that work and are tested together.
   
   The components include:
   
   * subctl
   * submariner-operator
   * submariner core (submariner engine, route agent, globalnet ipam)
   * lighthouse (lighthouse agent, lighthouse coredns)
   
  
## A release repository

   A release repository (submariner-io/releases) can be used to track and automate
   the whole release process.
   
   Yaml files in this repository can be used to control the definition of a release, 
   
   ```
      releases/0.5-<name>/0.5.0-<name>.yaml
      releases/0.5-<name>/0.5.1-bugfix for....yaml
      ...
   ```

## A release yaml

   A release yaml can have the following format:
   
   ```yaml
    ---
    version: 0.5.0
    name: the mighty 0.5 release!
    release-notes: 
      - bugfixes: >
        * we fixed this bug
      - features: >
        * this feature was added
        * and this other one
      - upgrades: >  # optional
        Upgrade notes important to anybody upgrading from a previous version
      - deprecations: > # optional
        Features or settings that have been deprecated and need to be updated
        by the administrator.

    components:
      - submariner: <commit-id> 
      - lighthouse: <commit-id>
      - submariner-operator: <commit-id>
      - submariner-charts: <commit-id>
      - shipyard: <commit-id>

   ```

## verification of the release yaml

The verification step would:
* Verify the format of the yaml file
* Verify that passed CI runs exist for the commit-ids proposed for the components
* Verify that submariner-operator dependencies to projects in go.mod are pointing
  to the proposed submariner, lighthouse, and shipyard releases, because this affects
  the behaviour of `subctl verify` to be up-to-date with the e2e tests of those repositories.
* Run E2E for the combination of commit-ids with helm & operator (with lighthouse enabled)


## merge of the release yaml

* A script will run in Github actions, executing step by step the tagging and verification
of the multiple components.

It will retry builds if necessary.

* Shipyard will be tagged to have a future reference to be able to rebuild the images.

* The release notes will be added to the website, proposed as a PR.


## Handling of versions.go in subctl/operator

Subctl and the operator have a set of version references about the other project they manage,
this is handled by updating a versions.go in the repository. For this management strategy
this will need to change in the following way

### "latest" reference

versions.go will reference latest by default in the source code, just as a placeholder
for anybody compiling subctl.

### references pinned at compilation

#### For a master build

For the [devel bleeding edge](https://github.com/submariner-io/submariner-operator/releases/tag/devel)
release channel, every component will be pinned at compilation time to the last successfully
published image of each component (race conditions apart we assume this is what we tested
in E2E). In the future we could make the release of subctl dependent on an e2e build, from
which we obtain the exact hash ids of the tested containers.

#### For a release build

In this case, when "merge of the release yaml" step is being handled, a tag for
submariner-operator will be generated, in this case, the subctl release will detect
a valid version tag (vx.x.x) and use that to pin all the different components at
compilation time.