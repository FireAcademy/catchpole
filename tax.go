package main

import (
    "log"
    "github.com/gofiber/fiber/v2"
)

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

func CheckAPIKeyAndReturnAPIKeyAndSubscribed(api_key string, max_credits int64) (*APIKey /*apiKey*/, bool /* subscribed */, bool /*errored*/) {
    // get data
    apiKey, weeklyUsage, subscribed := GetAPIKeyAndWeeklyUsage(api_key)

    if weeklyUsage == nil {
        weeklyUsage = CreateWeeklyUsage(api_key)
        if weeklyUsage == nil {
            return nil, false, true
        }
        apiKey, subscribed = GetAPIKeyAndSubscribed(api_key)
    }
    if apiKey == nil || apiKey.disabled {
        return nil, false, true
    }

    // limit check
    if apiKey.weekly_credit_limit != 0 && weeklyUsage.credits + max_credits > apiKey.weekly_credit_limit {
        return nil, false, true
    }

    // can user pay for max credits?
    if !subscribed && apiKey.free_credits_remaining < max_credits {
        return nil, false, true
    }
    
    return apiKey, subscribed, false
}

func TaxTraffic(apiKey *APIKey, subscribed bool, credits int64) bool /* errored */ {
    if apiKey.free_credits_remaining >= credits {
        if err := DecreaseAPIKeyFreeUsage(apiKey.api_key, credits); err != nil {
            log.Print(err)
            return true
        }
    } else {
        if !subscribed {
            return true
        }

        BillCredits(apiKey.api_key, apiKey.uid, credits)
    }

    err := IncreaseWeeklyUsage(apiKey.api_key, credits)
    if err != nil {
        log.Print(err)
        return true
    }

    return false
}

func LeafletTaxTrafficAndReturnOrigin(api_key string, credits_per_request int64) (string /*origin*/, bool /*errored*/) {
    apiKey, subscribed, error1 := CheckAPIKeyAndReturnAPIKeyAndSubscribed(api_key, credits_per_request)
    if error1 {
        return "", true
    }

    error2 := TaxTraffic(apiKey, subscribed, credits_per_request)

    return apiKey.origin, error2
} 
