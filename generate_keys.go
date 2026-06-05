//go:build ignore
// +build ignore

package main

import (
	"fmt"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		panic(err)
	}
	fmt.Println("VAPID_PRIVATE_KEY=" + privateKey)
	fmt.Println("VAPID_PUBLIC_KEY=" + publicKey)
}
