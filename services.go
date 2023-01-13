package main

import (
	"os"
	"log"
	"encoding/json"
)

var config Config

func getConfig() string {
    port := os.Getenv("CATCHPOLE_CONFIG")
   if port == "" {
       panic("CATCHPOLE_CONFIG not set")
   }

   return port
}

func SetupConfig() {
	config_str := getConfig()
	config = JSON.Unmarshal(config_str, &config)
}