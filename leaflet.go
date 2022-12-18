package main

import (
	"os"
    "fmt"
    "log"
    "bytes"
    "net/http"
    "io/ioutil"
    "github.com/gofiber/fiber/v2"
)

var leaflet_base_url string

func LeafletHandler(c *fiber.Ctx) error {
    api_key := GetAPIKeyForRequest(c)
    if api_key == "" {
        return MakeNoAPIKeyProvidedResponse(c)
    }    

    const CREDITS_PER_REQUEST = 420
    origin, errored := LeafletTaxTrafficAndReturnOrigin(api_key, CREDITS_PER_REQUEST)
    if errored {
        return MakeRequestBlockedResponse(c)
    }
    c.Set("Access-Control-Allow-Origin", origin)

    endpoint := c.Params("endpoint")
    url := fmt.Sprintf("%s/%s", leaflet_base_url, endpoint)

    resp, err := http.Post(url, "application/json", bytes.NewBuffer(c.Body()))
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "Leaflet: error ocurred when processing request")
    }
    defer resp.Body.Close()
    
    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "Leaflet: error ocurred when reading response")
    }
    c.Set("Content-Type", "application/json")
    return c.SendString(string(body))
}

func SetupLeafletBaseUrl() {
    leaflet_host := os.Getenv("CATCHPOLE_LEAFLET_HOST")
    if leaflet_host == "" {
        leaflet_host = "leaflet"
    }
    leaflet_port := os.Getenv("CATCHPOLE_LEAFLET_PORT")
    if leaflet_port == "" {
        leaflet_port = "18444"
    }
    leaflet_base_url = fmt.Sprintf("http://%s:%s", leaflet_host, leaflet_port)
    fmt.Printf("Leaflet at %s\n", leaflet_base_url)
}

func SetupLeafletRoutes(app *fiber.App) {
	SetupLeafletBaseUrl()

    app.Get("/leaflet/:endpoint", LeafletHandler)
    app.Post("/leaflet/:endpoint", LeafletHandler)
    app.Get("/:api_key<guid>/leaflet/:endpoint", LeafletHandler)
    app.Post("/:api_key<guid>/leaflet/:endpoint", LeafletHandler)
}