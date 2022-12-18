package main

import "github.com/gofiber/fiber/v2"

func MakeUnauthorizedResponse(c *fiber.Context, err string) error {
	return c.Status(401).JSON(fiber.Map{
		"message": err,
		"success": false,
	})
}

func MakeErrorResponse(c *fiber.Context, err string) error {
	return c.Status(500).JSON(fiber.Map{
		"message": err,
		"success": false,
	})
}

func MakeNoAPIKeyProvidedResponse(c *fiber.Context) error {
	return MakeUnauthorizedResponse(c, "No API key provided.")
}

func MakeRequestBlockedResponse(c *fiber.Context) error {
	return MakeUnauthorizedResponse(c, "Catchpole has blocked this request.")
}