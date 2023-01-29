package main

import (
	"io"
	"bytes"
	"errors"
	"context"
    "net/http"
    "io/ioutil"
    "encoding/json"
	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel/attribute"
	telemetry "github.com/fireacademy/telemetry"
	redis_mod "github.com/fireacademy/golden-gate/redis"
)

func MakeErrorResponse(c *fiber.Ctx, message string) error {
	_, span := telemetry.GetSpan(c.UserContext(), "MakeErrorResponse")
	span.SetAttributes(
		attribute.String("response.code", "418"),
		attribute.String("response.message", message),
	)
	defer span.End()

	return c.Status(418).JSON(fiber.Map{
		"success": false,
		"message": message,
	})
}

func MakeRequest(
	ctx context.Context,
	method string,
	url string,
	body []byte,
	header_keys []string,
	header_values []string,
) (*http.Response, error) {
	ctx, span := telemetry.GetSpan(ctx, "MakeRequest")
	defer span.End()

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
		return &http.Response{}, err
	}

	if method != "GET" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i, header_key := range header_keys {
		header_value := header_values[i]
		req.Header.Set(header_key, header_value)
	}

	res, err := client.Do(req)
	return res, err
}

type ResponseOfResultsBilledRequest struct {
    Results int64 `json:"results"`
}

func HandleRequest(c *fiber.Ctx) error {
	route_str := c.Params("route")
	path_str := c.Params("*")
	http_method := c.Method()

	ctx, span := telemetry.GetSpan(c.UserContext(), "HandleRequest")
	defer span.End()

	/* validate route & get info (+ cost) */
	ok1, route := GetRoute(route_str)
	if !ok1 {
		return MakeErrorResponse(c, "unknown route")
	}
	
	ok2, cost := GetCost(route_str, http_method, path_str)
	if !ok2 {
		return MakeErrorResponse(c, "unknown path")
	}

	/* parse service info and prepare to make request */
	service_base_url := route.BaseURL
	headers := route.Headers
	billing_method := route.BillingMethod

	header_values := make([]string, 0)
	for _, header := range headers {
		header_values = append(header_values, c.Get(header))
	}

	service_url := service_base_url + path_str

	span.SetAttributes(
		attribute.Int64("cost", cost),
		attribute.String("service_url", service_url),
		attribute.String("billing_method", billing_method),
	)

	/* check API key if this request is billed */
	have_to_bill := billing_method != "none"
	var api_key string
	if have_to_bill {
		api_key = GetAPIKeyForRequest(c)
		if api_key == "" {
			return MakeErrorResponse(c, "no API key provided")
		}

		ok, origin, err := CheckAPIKey(ctx, api_key)
		if err != nil {
			telemetry.LogError(ctx, err, "could not check API key")
			return MakeErrorResponse(c, "could not check API key")
		}
		if !ok {
			return MakeErrorResponse(c, "invalid or blocked API key")
		}
		c.Set("Access-Control-Allow-Origin", origin)
	}
	
	/* make request */
	resp, err := MakeRequest(ctx, http_method, service_url, c.Body(), headers, header_values)
	if err != nil {
		telemetry.LogError(ctx, err, "could not call internal service")
		return MakeErrorResponse(c, "error while calling internal service")
	}

	/* bill it */
	if billing_method == "per_request" {
		err = redis_mod.BillCreditsQuickly(ctx, api_key, cost)
		if err != nil {
			telemetry.LogError(
				ctx,
				errors.New(api_key + " not billed :|"),
				"could not bill user's request",
			)
		}
	} else if billing_method == "per_result" {
		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			telemetry.LogError(ctx, err, "could not read response")
			return MakeErrorResponse(c, "error ocurred while reading response")
		}

		billed_results := int64(1)

		billable_resp := new(ResponseOfResultsBilledRequest)
		err = json.Unmarshal(body, &billable_resp)
		if err != nil {
			telemetry.LogError(ctx, err, "could not decode response")
			return MakeErrorResponse(c, "error ocurred while decoding response")
		} else {
			if billable_resp.Results > 1 {
				billed_results = billable_resp.Results
			}
		}

		err = redis_mod.BillCreditsQuickly(ctx, api_key, cost * billed_results)
		if err != nil {
			telemetry.LogError(ctx, err, "could not bill request")
		}

		c.Set("Content-Type", "application/json")
		return c.SendString(string(body))
	}
	
	c.Set("Content-Type", "application/json")
	return c.Status(resp.StatusCode).SendStream(resp.Body)
}