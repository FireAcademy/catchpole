package main

import (
	"os"
    "fmt"
    "log"
    "bytes"
    "net/http"
    "io/ioutil"
    "encoding/json"
    "github.com/gofiber/fiber/v2"
)

var beta_base_url string
const BETA_CREDITS_PER_RESULT = 42
const BETA_MAX_RESULTS = 100

type BetaResponse struct {
    Results int64 `json:"results"`
}

func BetaHandler(c *fiber.Ctx) error {
    api_key := GetAPIKeyForRequest(c)
    if api_key == "" {
        return MakeNoAPIKeyProvidedResponse(c)
    }
    
    apiKey, subscribed, errored := CheckAPIKeyAndReturnAPIKeyAndSubscribed(api_key, BETA_MAX_RESULTS)
    if errored {
        return MakeRequestBlockedResponse(c)
    }
    c.Set("Access-Control-Allow-Origin", apiKey.origin)

    endpoint := c.Params("endpoint")
    url := fmt.Sprintf("%s/%s", beta_base_url, endpoint)

    resp, err := http.Post(url, "application/json", bytes.NewBuffer(c.Body()))
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "Beta: error ocurred when processing request")
    }
    defer resp.Body.Close()
    
    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "Beta: error ocurred when reading response")
    }

    // tax traffic
    var billed_results int64
    billed_results = 1

    betaResponse := new(BetaResponse)
    err = json.Unmarshal(body, &betaResponse)
    if err != nil {
        log.Print(err)
        return MakeErrorResponse(c, "Beta: error ocurred when decoding response")
    } else {
        if betaResponse.Results > 1 {
            billed_results = betaResponse.Results
        }
    }
    TaxTraffic(apiKey, subscribed, billed_results * int64(BETA_CREDITS_PER_RESULT))
    
    // send response
    c.Set("Content-Type", "application/json")
    return c.SendString(string(body))
}

func SetupBetaBaseUrl() {
    beta_host := os.Getenv("CATCHPOLE_BETA_HOST")
    if beta_host == "" {
        beta_host = "beta"
    }
    beta_port := os.Getenv("CATCHPOLE_BETA_PORT")
    if beta_port == "" {
        beta_port = "5000"
    }
    beta_base_url = fmt.Sprintf("http://%s:%s", beta_host, beta_port)
    fmt.Printf("Beta at %s\n", beta_base_url)
}

func SetupBetaRoutes(app *fiber.App) {
	SetupBetaBaseUrl()

    app.Get("/beta/:endpoint", BetaHandler)
    app.Post("/beta/:endpoint", BetaHandler)
    app.Get("/:api_key<guid>/beta/:endpoint", BetaHandler)
    app.Post("/:api_key<guid>/beta/:endpoint", BetaHandler)
}