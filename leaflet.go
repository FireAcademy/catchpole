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

func getAPIKeyForRequest(c *fiber.Ctx) string {
    api_key := c.Params("api_key")
    if api_key == "" {
        api_key = c.Get("X-API-Key")
    }
    if api_key == "" {
        api_key = c.Query("api-key")
    }

    return api_key
}

func leafletHandler(c *fiber.Ctx) error {
    api_key := getAPIKeyForRequest(c)
    if api_key == "" {
        return c.Status(401).SendString("No API key provided.")
    }    

    const CREDITS_PER_REQUEST = 420
    origin, errored := taxTrafficAndReturnOrigin(api_key, CREDITS_PER_REQUEST)
    if errored {
        return c.Status(401).SendString("Catchpole has blocked this request.")
    }
    c.Set("Access-Control-Allow-Origin", origin)

    endpoint := c.Params("endpoint")
    url := fmt.Sprintf("%s/%s", leaflet_base_url, endpoint)

    resp, err := http.Post(url, "application/json", bytes.NewBuffer(c.Body()))
    if err != nil {
        log.Print(err)
        return c.Status(500).SendString("Leaflet: error ocurred when processing request")
    }
    defer resp.Body.Close()
    
    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        log.Print(err)
        return c.Status(500).SendString("Leaflet: error ocurred when reading response")
    }
    c.Set("Content-Type", "application/json")
    return c.SendString(string(body))
}

func setupLeafletBaseUrl() {
    leaflet_host := os.Getenv("LEAFLET_HOST")
    if leaflet_host == "" {
        leaflet_host = "leaflet"
    }
    leaflet_port := os.Getenv("LEAFLET_PORT")
    if leaflet_port == "" {
        leaflet_port = "18444"
    }
    leaflet_base_url = fmt.Sprintf("http://%s:%s", leaflet_host, leaflet_port)
    fmt.Printf("Leaflet at %s\n", leaflet_base_url)
}

func setupLeafletRoutes(app *fiber.App) {
	setupLeafletBaseUrl()

    app.Get("/leaflet/:endpoint", leafletHandler)
    app.Post("/leaflet/:endpoint", leafletHandler)
    app.Get("/:api_key<guid>/leaflet/:endpoint", leafletHandler)
    app.Post("/:api_key<guid>/leaflet/:endpoint", leafletHandler)
}