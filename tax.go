package main

import (
    "log"
)

func taxTrafficAndReturnOrigin(api_key string, credits_per_request int64) (string /*origin*/, bool /*errored*/) {
    apiKey, weeklyUsage := getAPIKeyAndWeeklyUsage(api_key)

    if weeklyUsage == nil {
        weeklyUsage = createWeeklyUsage(api_key)
        apiKey = getAPIKey(api_key)
        if weeklyUsage == nil {
            return "", true
        }
    }
    if apiKey == nil || apiKey.disabled {
        return "", true
    }

    if apiKey.weekly_credit_limit != 0 && weeklyUsage.credits + credits_per_request > apiKey.weekly_credit_limit {
        return "", true
    }
    if apiKey.weekly_credit_limit == 0 && weeklyUsage.credits == 0 && apiKey.free_credits_remaining < credits_per_request && !getUser(apiKey.uid).has_active_stripe_subscription {
        return "", true
    }

    err := increaseWeeklyUsage(api_key, credits_per_request)
    if err != nil {
        return "", true
    }
    if apiKey.free_credits_remaining > credits_per_request {
        if err := decreaseAPIKeyFreeUsage(api_key, credits_per_request); err != nil {
            log.Print(err)
            return "", true
        }
    } else {
        billCredits(api_key, apiKey.uid, credits_per_request)
    }

    return apiKey.origin, false
} 
