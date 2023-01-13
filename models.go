package main

type Route struct {
	BaseURL string `json:"base_url"`
	Headers []string `json:"headers"`
	BillingMethod string `json:"billing_method"`
	Endpoints map[string]int32 `json:"endpoints"`
}

type Config = map[string]Route