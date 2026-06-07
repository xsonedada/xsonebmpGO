package main

import (
	"fmt"
	"net/http"
	"nexus-boost/database"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

func adminResolveDispute(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	// Только админ (ID=1) может решать споры
	if userID != 1 {
		c.Redirect(302, "/")
		return
	}

	disputeID := c.Param("id")
	decision := c.PostForm("decision")
	comment := c.PostForm("comment")

	// Получаем реальное имя админа
	var adminUsername string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&adminUsername)

	// Получаем данные спора
	var orderID, buyerID, boosterID int
	var total float64
	err := database.DB.QueryRow("SELECT order_id, user_id FROM disputes WHERE id = $1", disputeID).Scan(&orderID, &buyerID)
	if err != nil {
		c.Redirect(302, "/admin/disputes")
		return
	}
	database.DB.QueryRow("SELECT booster_id, total FROM orders WHERE id = $1", orderID).Scan(&boosterID, &total)

	// Обновляем диспут
	database.DB.Exec(
		"UPDATE disputes SET status = 'resolved', admin_decision = $1, resolved_at = NOW() WHERE id = $2",
		comment, disputeID,
	)

	// Сообщение о решении
	var decisionText string

	if decision == "refund" {
		// Возврат денег покупателю
		decisionText = "💰 Решение: возврат денег покупателю. " + comment

		database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", total, buyerID)
		database.DB.Exec("UPDATE orders SET status = 'refunded' WHERE id = $1", orderID)

		// История транзакций
		var buyerBalance float64
		database.DB.QueryRow("SELECT balance FROM users WHERE id = $1", buyerID).Scan(&buyerBalance)
		database.DB.Exec("INSERT INTO transaction_history (user_id, type, amount, balance_before, balance_after, description, order_id) VALUES ($1,'refund',$2,$3,$4,'Возврат по спору #'+$5,$6)",
			buyerID, total, buyerBalance-total, buyerBalance, disputeID, orderID)

		// Уведомления
		database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
			buyerID, fmt.Sprintf("✅ Возврат %.0f ₽ по спору #%s", total, disputeID), "/dispute/"+disputeID)
		go sendPushNotification(buyerID, "Спор решён", fmt.Sprintf("✅ Возврат %.0f ₽", total), "/dispute/"+disputeID)
		database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
			boosterID, fmt.Sprintf("❌ Спор #%s решен в пользу покупателя", disputeID), "/dispute/"+disputeID)
		go sendPushNotification(boosterID, "Спор решён", fmt.Sprintf("❌ В пользу покупателя"), "/dispute/"+disputeID)

	} else if decision == "complete" {
		// Заказ в пользу продавца
		decisionText = "✅ Решение: заказ выполнен, деньги переведены продавцу. " + comment

		// Выплата продавцу (с комиссией 5%)
		fee := total * 0.05
		sellerAmount := total - fee

		database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", sellerAmount, boosterID)
		database.DB.Exec("UPDATE orders SET status = 'completed' WHERE id = $1", orderID)
		database.DB.Exec("INSERT INTO platform_fees (order_id, amount, fee_percent) VALUES ($1,$2,5.00)", orderID, fee)

		// История транзакций продавца
		var sellerBalance float64
		database.DB.QueryRow("SELECT balance FROM users WHERE id = $1", boosterID).Scan(&sellerBalance)
		database.DB.Exec("INSERT INTO transaction_history (user_id, type, amount, balance_before, balance_after, description, order_id) VALUES ($1,'income',$2,$3,$4,'Выплата по спору #'+$5,$6)",
			boosterID, sellerAmount, sellerBalance-sellerAmount, sellerBalance, disputeID, orderID)

		// Уведомления
		database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
			boosterID, fmt.Sprintf("✅ Спор #%s решен в вашу пользу! +%.0f ₽", disputeID, sellerAmount), "/dispute/"+disputeID)
		go sendPushNotification(boosterID, "Спор решён", fmt.Sprintf("✅ В вашу пользу! +%.0f ₽", sellerAmount), "/dispute/"+disputeID)
		database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
			buyerID, fmt.Sprintf("❌ Спор #%s решен в пользу продавца", disputeID), "/dispute/"+disputeID)
		go sendPushNotification(buyerID, "Спор решён", "❌ В пользу продавца", "/dispute/"+disputeID)
	}

	// Сообщение админа в чат спора с реальным именем
	database.DB.Exec(
		"INSERT INTO dispute_messages (dispute_id, user_id, username, message, is_admin) VALUES ($1, $2, $3, $4, true)",
		disputeID, userID, adminUsername, decisionText,
	)

	database.DB.Exec("INSERT INTO admin_logs (admin_id, action, details) VALUES ($1, 'resolve_dispute', 'Решение по спору #'+$2)", userID, disputeID)

	c.Redirect(302, "/dispute/"+disputeID)
}

// Админка - все диспуты

func adminDisputesPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
        SELECT d.id, d.order_id, u.username, d.reason, d.status, d.created_at 
        FROM disputes d 
        JOIN users u ON d.user_id = u.id 
        ORDER BY d.created_at DESC
    `)
	if rows != nil {
		defer rows.Close()
	}
	user, _ := c.Get("user")

	type DisputeRow struct {
		ID        int
		OrderID   int
		Username  string
		Reason    string
		Status    string
		CreatedAt time.Time
	}

	var disputes []DisputeRow
	if rows != nil {
		for rows.Next() {
			var d DisputeRow
			rows.Scan(&d.ID, &d.OrderID, &d.Username, &d.Reason, &d.Status, &d.CreatedAt)
			disputes = append(disputes, d)
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-disputes",
		"User":   user,
		"Data":   gin.H{"Disputes": disputes},
	})
}

func adminVerificationsPage(c *gin.Context) {
	rows, _ := database.DB.Query(`
        SELECT v.id, v.user_id, u.username, v.full_name, v.description, v.contact, v.status, v.created_at 
        FROM verification_requests v 
        JOIN users u ON v.user_id = u.id 
        ORDER BY v.created_at DESC
    `)
	if rows != nil {
		defer rows.Close()
	}

	user, _ := c.Get("user")

	type VerificationRow struct {
		ID          int
		UserID      int
		Username    string
		FullName    string
		Description string
		Contact     string
		Status      string
		CreatedAt   time.Time
	}

	var verifications []VerificationRow
	if rows != nil {
		for rows.Next() {
			var v VerificationRow
			rows.Scan(&v.ID, &v.UserID, &v.Username, &v.FullName, &v.Description, &v.Contact, &v.Status, &v.CreatedAt)
			verifications = append(verifications, v)
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-verifications",
		"User":   user,
		"Data":   gin.H{"Verifications": verifications},
	})
}

// Одобрить верификацию

func adminApproveVerification(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	comment := c.PostForm("comment")

	var userID int
	database.DB.QueryRow("SELECT user_id FROM verification_requests WHERE id = $1", id).Scan(&userID)

	database.DB.Exec("UPDATE verification_requests SET status = 'approved', admin_comment = $1, reviewed_at = NOW() WHERE id = $2", comment, id)

	// Выдаем бейдж
	giveBadge(userID, "verified", "Верифицированный Продавец", "✅", "#10b981")

	// Уведомление
	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1, '✅ Ваша заявка на верификацию одобрена!', '/profile')", userID)

	c.Redirect(302, "/admin/verifications")
}

// Отклонить верификацию

func adminRejectVerification(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	comment := c.PostForm("comment")

	var userID int
	database.DB.QueryRow("SELECT user_id FROM verification_requests WHERE id = $1", id).Scan(&userID)

	database.DB.Exec("UPDATE verification_requests SET status = 'rejected', admin_comment = $1, reviewed_at = NOW() WHERE id = $2", comment, id)

	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1, '❌ Ваша заявка на верификацию отклонена', '/profile')", userID)

	c.Redirect(302, "/admin/verifications")
}

// Назначить роль
