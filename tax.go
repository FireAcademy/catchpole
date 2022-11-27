package main

import (
    "log"
    "github.com/gofiber/fiber/v2"
)

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

func taxTrafficAndReturnOrigin(api_key string, credits_per_request int64) (string /*origin*/, bool /*errored*/) {
    // get data
    apiKey, weeklyUsage, subscribed := getAPIKeyAndWeeklyUsage(api_key)

    if weeklyUsage == nil {
        weeklyUsage = createWeeklyUsage(api_key)
        if weeklyUsage == nil {
            return "", true
        }
        apiKey, subscribed = getAPIKeyAndSubscribed(api_key)
    }
    if apiKey == nil || apiKey.disabled {
        return "", true
    }

    // limit check
    if apiKey.weekly_credit_limit != 0 && weeklyUsage.credits + credits_per_request > apiKey.weekly_credit_limit {
        return "", true
    }

    // can user pay for these credits? + record usage
    if apiKey.free_credits_remaining > credits_per_request {
        if err := decreaseAPIKeyFreeUsage(api_key, credits_per_request); err != nil {
            log.Print(err)
            return "", true
        }
    } else {
        if !subscribed {
            return "", true
        }

        billCredits(api_key, apiKey.uid, credits_per_request)
    }
    err := increaseWeeklyUsage(api_key, credits_per_request)
    if err != nil {
        return "", true
    }

    return apiKey.origin, false
} 
