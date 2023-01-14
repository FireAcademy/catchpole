package main

import (
	"os"
	"fmt"
	"log"
	"bytes"
	"errors"
    "net/http"
    "io/ioutil"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/proxy"
)

func getPort() string {
    port := os.Getenv("CATCHPOLE_LISTEN_PORT")
   if port == "" {
       panic("CATCHPOLE_LISTEN_PORT not set")
   }

   return port
}

func Index(c *fiber.Ctx) error {
	return c.SendString("Catchpole is alive and well.")
}

func MakeErrorResponse(c *fiber.Ctx, message string) error {
	return c.Status(418).JSON(fiber.Map{
		"success": false,
		"message": message,
	})
}

func GetAPIKeyForRequest(c *fiber.Ctx) string {
    api_key := c.Params("api_key")
    if api_key == "" {
        api_key = c.Get("X-API-Key")
    }
    if api_key == "" {
        api_key = c.Query("api-key")
    }

    return api_key
}

func MakeRequest(
	c *fiber.Ctx,
	method string,
	url string,
	body []byte,
	header_keys []string,
	header_values []string,
) (http.Response, error) {
	client := &http.Client{}

	var body_buf io.Reader
	if method == "GET" {
		body_buf = nil
	} else {
		if len(body) < 2 {
			body = []byte("{}")
		}
		body_buf = bytes.NewBuffer(body)
	}
	req, err := http.NewRequest(method, url, body_buf)
	if err != nil {
		return http.Response{}, err
	}

	if method != "GET" {
		req.Headers.Set("Content-Type", "application/json")
	}
	for i, header_key := range header_keys {
		header_value := header_vales[i]
		req.Headers.Set(header_key, header_value)
	}

	res, err := client.Do(req)
	return res, err
}

func HandleRequest(c *fiber.Ctx) error {
	route_str := c.Params("route")
	path_str := c.Params("*")
	http_method := c.Method()

	/* Validate route & get info (+ cost) */
	ok1, route := getRoute(route_str)
	if !ok1 {
		return MakeErrorResponse("unknown route")
	}
	
	ok2, cost := getCost(route_str, http_method, path_str)
	if !ok2 {
		return MakeErrorResponse("unknown path")
	}

	/* parse service info and prepare to make request */
	service_base_url := route.BaseURL
	headers := route.Headers
	billing_method := route.BillingMethod

	header_values = make([]string)
	for i, header := range headers {
		header_values = append(header_values, c.Get(headers[i]))
	}

	service_url = service_base_url + parh_str

	if billing_method == "per_request" {
		api_key := GetAPIKeyForRequest()
		if api_key == "" {
			return MakeErrorResponse("no API key provided")
		}

		// TODO: check API key / bill

		resp, err := MakeRequest(c, http_method, service_url, c.Body(), headers, header_values)
		if err != nil {
			log.Print(err)
			return MakeErrorResponse("error while calling internal service")
		}
	} else if billing_method == "per_result" {
		// TODO:
	} else if billing_method == "none" {
		// TODO;
	}

	return MakeErrorResponse("internal error ocurred")
}

func main() {
	SetupConfig()
	app := fiber.New()

	app.Get("/", Index)
	app.Get("/:api_key<guid>/:route/*", HandleRequest)
	app.Post("/:api_key<guid>/:route/*", HandleRequest)
	app.Put("/:api_key<guid>/:route/*", HandleRequest)
	app.Get("/:route/*", HandleRequest)
	app.Post("/:route/*", HandleRequest)
	app.Put("/:route/*", HandleRequest)

	port := getPort()
	log.Fatalln(app.Listen(fmt.Sprintf(":%v", port)))
}