package main

import (
    "time"
    "github.com/stripe/stripe-go/v74"
    "github.com/stripe/stripe-go/v74/usagerecord"
)

func billEveryone() {
    itemId, credits := getUserBillingInfo()
    for itemId != "" {
        params := &stripe.UsageRecordParams{
            SubscriptionItem: stripe.String(itemId),
            Quantity: stripe.Int64(credits),
            Timestamp: stripe.Int64(time.Now().Unix()),
            Action: stripe.String(string(stripe.UsageRecordActionSet)),
        }

        usagerecord.New(params)

        itemId, credits = getUserBillingInfo()
    }
}

func stripeBillRoutine() {
    time.Sleep(10 * time.Second) // allow everything to boot up
    for true {
        billEveryone()
        time.Sleep(5 * time.Minute)
    }
}