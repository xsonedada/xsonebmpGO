package main

import (
	cryptorand "crypto/rand"
	"math/rand"
	"net/http"
	"nexus-boost/database"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var loginMu sync.Mutex

func markAllReadHandler(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"success": false})
		return
	}

	database.DB.Exec("UPDATE notifications SET is_read = true WHERE user_id = $1", userID)
	c.JSON(200, gin.H{"success": true})
}

func markOneReadHandler(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.JSON(403, gin.H{"success": false})
		return
	}

	notifID := c.Param("id")
	database.DB.Exec("UPDATE notifications SET is_read = true WHERE id = $1 AND user_id = $2", notifID, userID)
	c.JSON(200, gin.H{"success": true})
}

func transactionsPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	rows, _ := database.DB.Query(`
        SELECT id, type, amount, balance_before, balance_after, description, created_at 
        FROM transaction_history 
        WHERE user_id = $1 
        ORDER BY created_at DESC LIMIT 50
    `, userID)
	if rows != nil {
		defer rows.Close()
	}

	type Transaction struct {
		ID            int
		Type          string
		Amount        float64
		BalanceBefore float64
		BalanceAfter  float64
		Description   string
		CreatedAt     time.Time
	}

	var transactions []Transaction
	if rows != nil {
		for rows.Next() {
			var t Transaction
			rows.Scan(&t.ID, &t.Type, &t.Amount, &t.BalanceBefore, &t.BalanceAfter, &t.Description, &t.CreatedAt)
			transactions = append(transactions, t)
		}
	}

	var user User
	database.DB.QueryRow("SELECT id, username, balance FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username, &user.Balance)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "transactions",
		"Data":   gin.H{"Transactions": transactions},
	})
}

func getTransactions(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	rows, _ := database.DB.Query(
		"SELECT id, type, amount, description, created_at FROM transaction_history WHERE user_id = $1 ORDER BY created_at DESC LIMIT 20",
		userID,
	)
	if rows != nil {
		defer rows.Close()
	}

	var transactions []gin.H
	if rows != nil {
		for rows.Next() {
			var id int
			var ttype, desc string
			var amount float64
			var createdAt time.Time
			rows.Scan(&id, &ttype, &amount, &desc, &createdAt)
			transactions = append(transactions, gin.H{"id": id, "type": ttype, "amount": amount, "description": desc, "created_at": createdAt})
		}
	}

	c.JSON(200, transactions)
}

// Функция:

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	if _, err := cryptorand.Read(b); err != nil {
		for i := range b {
			b[i] = letters[rand.Intn(len(letters))]
		}
	} else {
		for i := range b {
			b[i] = letters[int(b[i])%len(letters)]
		}
	}
	return string(b)
}

var httpClient = &http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:    10,
		IdleConnTimeout: 30 * time.Second,
	},
}
