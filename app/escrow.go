package main

import (
	"fmt"
	"net/http"
	"nexus-boost/database"
	"time"

	"github.com/gin-gonic/gin"
)

func escrowRefund(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		c.Redirect(302, "/login")
		return
	}
	if !validateUserCSRF(c) {
		c.Redirect(302, "/seller/orders?error=Ошибка+безопасности")
		return
	}
	orderID := c.Param("orderId")
	var buyerID, sellerID int
	var amount float64
	err := database.DB.QueryRow(
		"SELECT buyer_id, seller_id, amount FROM escrow_transactions WHERE order_id = $1 AND status = 'frozen'",
		orderID,
	).Scan(&buyerID, &sellerID, &amount)
	if err != nil || sellerID != userID {
		c.Redirect(302, "/seller/orders")
		return
	}
	database.DB.Exec("UPDATE escrow_transactions SET status = 'refunded', refunded_at = NOW() WHERE order_id = $1", orderID)
	database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", amount, buyerID)
	database.DB.Exec("UPDATE orders SET status = 'refunded' WHERE id = $1", orderID)
	c.Redirect(302, "/seller/order/"+orderID)
}

func escrowPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	// Покупки (где пользователь - покупатель)
	buyRows, _ := database.DB.Query(`
        SELECT e.id, e.order_id, e.amount, e.status, e.frozen_at, o.title, u.username as seller_name
        FROM escrow_transactions e
        JOIN orders o ON e.order_id = o.id
        JOIN users u ON e.seller_id = u.id
        WHERE e.buyer_id = $1
        ORDER BY e.frozen_at DESC
    `, userID)
	if buyRows != nil {
		defer buyRows.Close()
	}

	// Продажи (где пользователь - продавец)
	sellRows, _ := database.DB.Query(`
        SELECT e.id, e.order_id, e.amount, e.status, e.frozen_at, o.title, u.username as buyer_name
        FROM escrow_transactions e
        JOIN orders o ON e.order_id = o.id
        JOIN users u ON e.buyer_id = u.id
        WHERE e.seller_id = $1
        ORDER BY e.frozen_at DESC
    `, userID)
	if sellRows != nil {
		defer sellRows.Close()
	}

	type EscrowItem struct {
		ID       int
		OrderID  int
		Amount   float64
		Status   string
		FrozenAt time.Time
		Title    string
		Name     string
	}

	var purchases []EscrowItem
	if buyRows != nil {
		for buyRows.Next() {
			var e EscrowItem
			buyRows.Scan(&e.ID, &e.OrderID, &e.Amount, &e.Status, &e.FrozenAt, &e.Title, &e.Name)
			purchases = append(purchases, e)
		}
	}

	var sales []EscrowItem
	if sellRows != nil {
		for sellRows.Next() {
			var e EscrowItem
			sellRows.Scan(&e.ID, &e.OrderID, &e.Amount, &e.Status, &e.FrozenAt, &e.Title, &e.Name)
			sales = append(sales, e)
		}
	}

	var user User
	database.DB.QueryRow("SELECT id, username, balance FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username, &user.Balance)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "escrow",
		"Data":   gin.H{"Purchases": purchases, "Sales": sales},
	})
}

// Статус эскроу

func escrowStatus(c *gin.Context) {
	orderID := c.Param("orderId")

	var status string
	var amount float64
	database.DB.QueryRow("SELECT status, amount FROM escrow_transactions WHERE order_id = $1", orderID).Scan(&status, &amount)

	c.JSON(200, gin.H{"status": status, "amount": amount})
}

// Выплата продавцу

func releaseEscrow(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	orderID := c.Param("orderId")

	var buyerID, sellerID int
	var amount float64
	var status string
	err := database.DB.QueryRow(
		"SELECT buyer_id, seller_id, amount, status FROM escrow_transactions WHERE order_id = $1", orderID,
	).Scan(&buyerID, &sellerID, &amount, &status)

	if err != nil {
		c.JSON(404, gin.H{"success": false, "message": "Эскроу-транзакция не найдена"})
		return
	}

	if status != "frozen" && status != "ready" {
		c.JSON(400, gin.H{"success": false, "message": "Средства уже не заморожены"})
		return
	}

	if buyerID != userID {
		c.JSON(403, gin.H{"success": false, "message": "Только покупатель может подтвердить"})
		return
	}

	var feePercent float64 = 5.0
	var isPro bool
	database.DB.QueryRow("SELECT COALESCE(is_pro, false) FROM seller_profiles WHERE user_id = $1", sellerID).Scan(&isPro)
	if isPro {
		feePercent = 2.0
	}
	fee := amount * feePercent / 100
	sellerAmount := amount - fee

	tx, err := database.DB.Begin()
	if err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}
	defer tx.Rollback()

	if _, err = tx.Exec("UPDATE escrow_transactions SET status = 'released', released_at = NOW() WHERE order_id = $1 AND status IN ('frozen', 'ready')", orderID); err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}
	if _, err = tx.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", sellerAmount, sellerID); err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}
	if _, err = tx.Exec("INSERT INTO platform_fees (order_id, amount, fee_percent) VALUES ($1,$2,$3)", orderID, fee, feePercent); err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}
	if _, err = tx.Exec("UPDATE orders SET status = 'completed' WHERE id = $1", orderID); err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}
	if _, err = tx.Exec("INSERT INTO messages (order_id, user_id, username, text) VALUES ($1,$2,'Система','✅ Покупатель подтвердил выполнение заказа. Средства переведены продавцу.')",
		orderID, buyerID); err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}

	var sellerBalanceBefore float64
	if err = tx.QueryRow("SELECT balance FROM users WHERE id = $1", sellerID).Scan(&sellerBalanceBefore); err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}
	if _, err = tx.Exec("INSERT INTO transaction_history (user_id, type, amount, balance_before, balance_after, description, order_id) VALUES ($1,'income',$2,$3,$4,'Выплата по заказу',$5)",
		sellerID, sellerAmount, sellerBalanceBefore-sellerAmount, sellerBalanceBefore, orderID); err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}
	if _, err = tx.Exec("UPDATE users SET orders = orders + 1 WHERE id = $1", sellerID); err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}

	if err = tx.Commit(); err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}

	checkAndGiveBadges(sellerID)

	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
		sellerID, fmt.Sprintf("💰 Выплата %.0f ₽ по заказу #%s", sellerAmount, orderID), "/seller/orders")
	go sendPushNotification(sellerID, "Выплата", fmt.Sprintf("💰 +%.0f ₽", sellerAmount), "/seller/orders")

	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
		buyerID, fmt.Sprintf("✅ Заказ #%s подтвержден", orderID), "/order/"+orderID)
	go sendPushNotification(buyerID, "Заказ подтверждён", fmt.Sprintf("✅ Заказ #%s", orderID), "/order/"+orderID)

	c.JSON(200, gin.H{"success": true, "message": "Заказ подтвержден! Средства переведены продавцу."})
}

// Возврат покупателю

func refundEscrow(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	orderID := c.Param("orderId")

	// Проверяем что админ или покупатель
	_, isAdmin := session.Values["admin_id"]

	var buyerID int
	var amount float64
	database.DB.QueryRow("SELECT buyer_id, amount FROM escrow_transactions WHERE order_id = $1 AND status = 'frozen'", orderID).
		Scan(&buyerID, &amount)

	if buyerID != userID.(int) && !isAdmin {
		c.JSON(403, gin.H{"success": false, "message": "Нет прав на возврат"})
		return
	}

	// Возвращаем деньги покупателю
	database.DB.Exec("UPDATE escrow_transactions SET status = 'refunded', refunded_at = NOW() WHERE order_id = $1", orderID)
	database.DB.Exec("UPDATE users SET balance = balance + $1 WHERE id = $2", amount, buyerID)
	database.DB.Exec("UPDATE orders SET status = 'refunded' WHERE id = $1", orderID)

	// История транзакций покупателя
	var buyerBalance float64
	database.DB.QueryRow("SELECT balance FROM users WHERE id = $1", buyerID).Scan(&buyerBalance)
	database.DB.Exec("INSERT INTO transaction_history (user_id, type, amount, balance_before, balance_after, description, order_id) VALUES ($1,'refund',$2,$3,$4,'Возврат по заказу',$5)",
		buyerID, amount, buyerBalance-amount, buyerBalance, orderID)

	// Уведомление покупателю
	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
		buyerID,
		fmt.Sprintf("↩️ Возврат %.0f ₽ по заказу #%s", amount, orderID),
		"/escrow")

	c.JSON(200, gin.H{"success": true, "message": "Средства возвращены покупателю"})
}

// Страница истории транзакций
