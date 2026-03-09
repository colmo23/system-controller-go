#!/bin/bash

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
${DIR}/../system-controller --log ${DIR}/test.log ${DIR}/inventory-small-2.ini ${DIR}/services.yaml 
