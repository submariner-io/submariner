<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

- [Contributing to Submariner](#contributing-to-submariner)
  - [Overview](#overview)
  - [How to contribute](#how-to-contribute)
  - [Reaching out](#reaching-out)
    - [slack](#slack)
    - [Google group](#google-group)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Contributing to Submariner

:+1::tada: First off, thanks for taking the time to contribute to Submariner! :tada::+1:

## Overview

Submariner is a tool built to connect overlay networks of different Kubernetes clusters. It aims to be CNI agnostic.

## How to contribute

To contribute to Submariner:

1. Fork this repository
2. Make your changes in the fork
3. Test your changes by running `make e2e` which deploys [Submariner in a Kind 3 cluster setup](https://github.com/submariner-io/submariner/blob/master/scripts/kind-e2e/README.md) and runs E2E tests
4. Submit a pull request (PR) to Submariner master branch
5. (Optionally) Run `make cleanup` to tear down the test clusters once you're finished.

Refer to [this link](https://help.github.com/en/articles/creating-a-pull-request-from-a-fork) on how to submit a PR.

A good PR commit message clearly explains the purpose of a PR and the logic implemented.

## Reaching out

You can reach out to Submariner folks on Slack and Google Groups.

### Slack

Join the #submariner Slack channel on [the Kubernetes workspace](https://kubernetes.slack.com/).

### Google group

Join [Submariner's Google group](https://groups.google.com/forum/#!forum/submariner-dev).

### Weekly Meeting

Join [Submariner's weekly meeting](https://bluejeans.com/3472508766) from 11:00-11:30AM ET ([agendas and notes](https://docs.google.com/document/d/1qnZ2LpF_rXGfnYYPNTldQ4WbeEUxwnuQD-xTC6GbZdg)).
