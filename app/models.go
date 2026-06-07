package main

type pushSubscription struct {
	Endpoint  string `json:"endpoint"`
	Auth      string `json:"auth"`
	P256dh    string `json:"p256dh"`
	UserAgent string `json:"user_agent"`
}
