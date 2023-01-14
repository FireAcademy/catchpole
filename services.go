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

func GetRoute(route string) (bool /* exists */, Route) {
	r, ok := config[route]
	return ok, r
}

func GetCost(route string, method string, path string) (bool /* exists */, int32) {
	ok, r := getRoute(route)
	if !ok {
		return false, 0
	}

	cost, ok := r.Endpoints[method + "." + path]
	if !ok {
		cost, ok = r.Endpoints["*"]
		if !ok {
			return false, 0
		}

		return true, cost
	}

	return true, cost
}

func SetupConfig() {
	config_str := getConfig()
	err := json.Unmarshal([]byte(config_str), &config)

	if err != nil {
		log.Print(err)
		panic(err)
	}
}