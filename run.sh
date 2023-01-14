#!/bin/bash

export CATCHPOLE_CONFIG=$(cat prices.json)
go run .
