#!/bin/bash

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
${DIR}/../system-controller --log ${DIR}/test.log ${DIR}/inventory-small.ini ${DIR}/services.yaml 
