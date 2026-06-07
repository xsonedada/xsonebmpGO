package main

import (
	"net/http"
	"nexus-boost/database"
	"time"

	"github.com/gin-gonic/gin"
)

func referralPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	var refCode string
	err := database.DB.QueryRow("SELECT id, username, balance, COALESCE(referral_code, '') FROM users WHERE id = $1", userID).
		Scan(&user.ID, &user.Username, &user.Balance, &refCode)

	if err != nil {
		c.Redirect(302, "/login")
		return
	}

	// Если реферального кода нет — генерируем
	if refCode == "" {
		refCode = randomString(8)
		database.DB.Exec("UPDATE users SET referral_code = $1 WHERE id = $2", refCode, userID)
	}

	// Прямые рефералы
	var directCount int
	var directEarnings float64
	database.DB.QueryRow("SELECT COUNT(*), COALESCE(SUM(earnings), 0) FROM referrals WHERE referrer_id = $1 AND level = 1", userID).
		Scan(&directCount, &directEarnings)

	// Рефералы второго уровня
	var level2Count int
	var level2Earnings float64
	database.DB.QueryRow("SELECT COUNT(*), COALESCE(SUM(earnings), 0) FROM referrals WHERE referrer_id = $1 AND level = 2", userID).
		Scan(&level2Count, &level2Earnings)

	// История заработка
	earnings := []gin.H{}
	rows, err := database.DB.Query(`
		SELECT re.amount, re.level, re.created_at, u.username 
		FROM referral_earnings re 
		JOIN users u ON re.from_user_id = u.id 
		WHERE re.user_id = $1 
		ORDER BY re.created_at DESC LIMIT 20
	`, userID)

	if err == nil && rows != nil {
		defer rows.Close()
		for rows.Next() {
			var amount float64
			var level int
			var createdAt time.Time
			var username string
			rows.Scan(&amount, &level, &createdAt, &username)
			earnings = append(earnings, gin.H{
				"Amount":    amount,
				"Level":     level,
				"CreatedAt": createdAt,
				"Username":  username,
			})
		}
	}

	refLink := "https://xsonebmp.ru?ref=" + refCode

	layoutHTML(c, http.StatusOK, gin.H{
		"User":           &user,
		"Active":         "referral",
		"RefCode":        refCode,
		"RefLink":        refLink,
		"DirectCount":    directCount,
		"DirectEarnings": directEarnings,
		"Level2Count":    level2Count,
		"Level2Earnings": level2Earnings,
		"Earnings":       earnings,
	})
}
