#!/bin/bash

docker tag kubevirtci/ocp-4.4-provision:latest quay.io/kubevirtci/ocp-4.4:latest
docker push quay.io/kubevirtci/ocp-4.4:latest
