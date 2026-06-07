package main

import (
	"encoding/json"
	"io"
	"log"
	"nexus-boost/database"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/gin-gonic/gin"
)

func pushSubscribe(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"error": "auth required"})
		return
	}

	body, _ := io.ReadAll(c.Request.Body)
	var sub pushSubscription
	json.Unmarshal(body, &sub)

	database.DB.Exec(`
        INSERT INTO push_subscriptions (user_id, endpoint, auth, p256dh, user_agent) 
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (user_id, endpoint) DO UPDATE SET auth=$3, p256dh=$4, updated_at=NOW()
    `, userID, sub.Endpoint, sub.Auth, sub.P256dh, sub.UserAgent)

	c.JSON(200, gin.H{"success": true})
}

func sendPushNotification(userID int, title, body, url string) {
	rows, err := database.DB.Query(
		"SELECT endpoint, auth, p256dh FROM push_subscriptions WHERE user_id = $1",
		userID,
	)
	if err != nil {
		log.Printf("Push query error: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var endpoint, auth, p256dh string
		rows.Scan(&endpoint, &auth, &p256dh)

		s := &webpush.Subscription{
			Endpoint: endpoint,
			Keys: webpush.Keys{
				Auth:   auth,
				P256dh: p256dh,
			},
		}

		payload, _ := json.Marshal(map[string]string{
			"title": title,
			"body":  body,
			"url":   url,
		})

		_, err := webpush.SendNotification(payload, s, &webpush.Options{
			Subscriber:      "xsonebmp@example.com",
			VAPIDPublicKey:  vapidPublicKey,
			VAPIDPrivateKey: vapidPrivateKey,
			TTL:             30,
		})
		if err != nil {
			log.Printf("Push send error: %v", err)
			database.DB.Exec("DELETE FROM push_subscriptions WHERE endpoint = $1", endpoint)
		}
	}
}

// ============ QR-ВХОД ============
