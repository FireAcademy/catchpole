#!/bin/bash

go install fireacademy/taxman
export PATH=$PATH:$(dirname $(go list -f '{{.Target}}' .))
taxman
