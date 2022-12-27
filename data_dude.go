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

var data_dude_base_url string

func DataDudeHandler(c *fiber.Ctx) error {
    endpoint := c.Params("endpoint")
    url := fmt.Sprintf("%s/%s", data_dude_base_url, endpoint)

    resp, err := http.Post(url, "application/json", bytes.NewBuffer(c.Body()))
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "data-dude: error ocurred when processing request")
    }
    defer resp.Body.Close()
    
    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "data-dude: error ocurred when reading response")
    }
    c.Set("Content-Type", "application/json")
    return c.SendString(string(body))
}

func SetupDataDudeBaseUrl() {
    data_dude_base_url = os.Getenv("DATA_DUDE_BASE_URL")
    if data_dude_base_url == "" {
        panic("DATA_DUDE_BASE_URL not set")
    }
    fmt.Printf("data-dude at %s\n", leaflet_base_url)
}

func SetupDataDudeRoutes(app *fiber.App) {
	SetupDataDudeBaseUrl()

    app.Get("/leaflet/:endpoint", DataDudeHandler)
    app.Post("/leaflet/:endpoint", DataDudeHandler)
}