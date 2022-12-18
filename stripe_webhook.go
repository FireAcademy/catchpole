package main

import (
    "os"
    "fmt"
    "log"
    "github.com/gofiber/fiber/v2"
    "github.com/stripe/stripe-go/v74"
    "github.com/stripe/stripe-go/v74/webhook"
    "github.com/stripe/stripe-go/v74/customer"
    "github.com/stripe/stripe-go/v74/subscription"
)

func stripeWebhook(c *fiber.Ctx) error {
    stripe_webhook_secret := os.Getenv("STRIPE_WEBHOOK_SECRET")
    if stripe_webhook_secret == "" {
        fmt.Printf("STRIPE_WEBHOOK_SECRET not specified - this is BAD!")
        return MakeErrorResponse(c, "not ok ser")
    }

    event, err := webhook.ConstructEvent(c.Body(), c.Get("Stripe-Signature"), stripe_webhook_secret)
    if err != nil {
        log.Print(err)
        return MakeUnauthorizedResponse(c, "not ok ser")
    }

    switch event.Type {
        case "checkout.session.completed":
            if event.Data.Object["mode"].(string) != "subscription" {
                log.Print("skipping checkout.session.completed since mode is not 'subscription'")
                log.Print("mode is: " + event.Data.Object["mode"].(string))
                log.Print(event)
                break
            }
            customerId := event.Data.Object["customer"].(string)
            subscriptionId := event.Data.Object["subscription"].(string)
            s, _ := subscription.Get(subscriptionId, nil)

            item := s.Items.Data[0]
            itemId := item.ID
            productId := item.Plan.ID
            priceId := item.Plan.Product.ID

            customer, err := customer.Get(customerId, nil)
            if err != nil {
                log.Print(err);
                return MakeErrorResponse(c, "error #1 ser")
            }
            uid := customer.Metadata["uid"]
            err = updateCustomerBillingDetails(uid, true, customerId, itemId, subscriptionId, productId, priceId)
            if err != nil {
                log.Print(err);
                return MakeErrorResponse(c, "error #2 ser")
            }
            break;
        case "invoice.paid":
            customerId := event.Data.Object["customer"].(string)
            
            err := updateCustomerActiveSubscription(customerId, true);
            if err != nil {
                log.Print(err);
                return MakeErrorResponse(c, "error ser")
            }
            break;
        case "invoice.payment_failed":
            customerId := event.Data.Object["customer"].(string)
            customer, err := customer.Get(customerId, nil)
            if err != nil {
                log.Print(err);
                return MakeErrorResponse(c, "error #1 ser")
            }
            uid := customer.Metadata["uid"]

            err = updateCustomerActiveSubscription(customerId, false);
            if err != nil {
                log.Print(err);
                return MakeErrorResponse(c, "error #2 ser")
            }

            err = revokeAPIKeys(uid);
            if err != nil {
                log.Print(err);
                return MakeErrorResponse(c, "error #3 ser")
            }
            break;
        case "customer.subscription.deleted":
            customerId := event.Data.Object["customer"].(string)
            customer, err := customer.Get(customerId, nil)
            if err != nil {
                log.Print(err);
                return MakeErrorResponse(c, "error #1 ser")
            }
            uid := customer.Metadata["uid"]
            
            err = updateCustomerBillingDetails(uid, false, customerId, "", "", "", "")
            if err != nil {
                log.Print(err);
                return MakeErrorResponse(c, "error #2 ser")
            }

            err = revokeAPIKeys(uid);
            if err != nil {
                log.Print(err);
                return MakeErrorResponse(c, "error #3 ser")
            }
            break;
        default:
            return c.Status(200).SendString("wat am I supposed to do with dat?!")
    }

    return c.SendString("ok ser")
}

func setupStripeWebhook(app *fiber.App) {
    stripe_token := os.Getenv("STRIPE_SECRET_KEY")
    if stripe_token == "" {
        panic("STRIPE_SECRET_KEY not set")
    }
    
    stripe.Key = stripe_token
    app.Post("/stripe/webhook", stripeWebhook)
}