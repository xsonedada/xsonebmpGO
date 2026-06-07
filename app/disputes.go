package main

import (
	"fmt"
	"net/http"
	"nexus-boost/database"
	"time"

	"github.com/gin-gonic/gin"
)

func getDisputeMessages(c *gin.Context) {
	user := userFromContext(c)
	if user == nil {
		c.JSON(403, gin.H{"error": "Требуется авторизация"})
		return
	}
	disputeID := c.Param("id")
	if !disputeParticipant(user.ID, disputeID) && !isAdminSession(c) {
		c.JSON(403, gin.H{"error": "Доступ запрещён"})
		return
	}

	rows, _ := database.DB.Query(
		"SELECT id, user_id, username, message, is_admin, created_at FROM dispute_messages WHERE dispute_id = $1 ORDER BY created_at ASC",
		disputeID,
	)
	if rows != nil {
		defer rows.Close()
	}
	var msgs []gin.H
	if rows != nil {
		for rows.Next() {
			var id, uid int
			var username, message string
			var isAdmin bool
			var createdAt time.Time
			rows.Scan(&id, &uid, &username, &message, &isAdmin, &createdAt)
			msgs = append(msgs, gin.H{
				"id": id, "user_id": uid, "username": username,
				"message": message, "is_admin": isAdmin, "created_at": createdAt,
			})
		}
	}
	c.JSON(200, msgs)
}

func openDisputePage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	orderID := c.Param("id")

	// Проверяем что пользователь - покупатель ИЛИ продавец
	var isBuyer, isSeller bool
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM orders WHERE id = $1 AND user_id = $2)", orderID, userID).Scan(&isBuyer)
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM orders WHERE id = $1 AND booster_id = $2)", orderID, userID).Scan(&isSeller)

	if !isBuyer && !isSeller {
		c.String(404, "Заказ не найден")
		return
	}

	// Проверяем что заказ существует
	var o Order
	err := database.DB.QueryRow(
		"SELECT id, title, total, status FROM orders WHERE id = $1", orderID,
	).Scan(&o.ID, &o.Title, &o.Total, &o.Status)

	if err != nil {
		c.Redirect(302, "/profile?error=Заказ+не+найден")
		return
	}

	// Проверяем что диспут еще не открыт
	var disputeID int
	database.DB.QueryRow("SELECT COALESCE(id, 0) FROM disputes WHERE order_id = $1", orderID).Scan(&disputeID)
	if disputeID > 0 {
		c.Redirect(302, "/dispute/"+fmt.Sprintf("%d", disputeID))
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "dispute-open",
		"Data":   gin.H{"Order": o},
	})
}

// Создание диспута

func createDispute(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	orderID := c.Param("id")
	if !orderParticipant(userID, orderID) {
		c.Redirect(302, "/profile?error=Заказ+не+найден")
		return
	}

	reason := c.PostForm("reason")
	description := c.PostForm("description")

	var disputeID int
	err := database.DB.QueryRow(
		"INSERT INTO disputes (order_id, user_id, reason, description) VALUES ($1,$2,$3,$4) RETURNING id",
		orderID, userID, reason, description,
	).Scan(&disputeID)

	if err != nil {
		c.Redirect(302, "/profile?error=Ошибка+создания+диспута")
		return
	}

	// Обновляем статус заказа
	database.DB.Exec("UPDATE orders SET status = 'disputed' WHERE id = $1", orderID)

	// Уведомление продавцу
	var boosterID int
	database.DB.QueryRow("SELECT booster_id FROM orders WHERE id = $1", orderID).Scan(&boosterID)

	notifText := fmt.Sprintf("⚠️ Открыт диспут по заказу #%s", orderID)
	database.DB.Exec("INSERT INTO notifications (user_id, text, link) VALUES ($1,$2,$3)",
		boosterID, notifText, "/dispute/"+fmt.Sprintf("%d", disputeID))
	go sendPushNotification(boosterID, "Открыт диспут", notifText, "/dispute/"+fmt.Sprintf("%d", disputeID))

	c.Redirect(302, "/dispute/"+fmt.Sprintf("%d", disputeID))
}

// Страница диспута

func disputeDetailPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	disputeID := c.Param("id")

	var dispute struct {
		ID            int
		OrderID       int
		UserID        int
		Reason        string
		Description   string
		Status        string
		AdminDecision string
		CreatedAt     time.Time
	}

	err := database.DB.QueryRow(
		"SELECT id, order_id, user_id, reason, description, status, COALESCE(admin_decision, ''), created_at FROM disputes WHERE id = $1",
		disputeID,
	).Scan(&dispute.ID, &dispute.OrderID, &dispute.UserID, &dispute.Reason, &dispute.Description, &dispute.Status, &dispute.AdminDecision, &dispute.CreatedAt)

	if err != nil {
		c.String(404, "Спор не найден")
		return
	}

	// Проверяем доступ: админ (ID=1), покупатель ИЛИ продавец
	isAdminUser := userID == 1
	isBuyer := userID == dispute.UserID

	var isSeller bool
	database.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM orders WHERE id = $1 AND booster_id = $2)", dispute.OrderID, userID).Scan(&isSeller)

	if !isAdminUser && !isBuyer && !isSeller {
		c.Redirect(302, "/profile")
		return
	}

	// Если админ зашел - оставляем системное сообщение
	if isAdminUser {
		var adminMsgCount int
		database.DB.QueryRow(
			"SELECT COUNT(*) FROM dispute_messages WHERE dispute_id = $1 AND is_admin = true AND message LIKE '👑%'",
			disputeID,
		).Scan(&adminMsgCount)

		if adminMsgCount == 0 {
			var adminUsername string
			database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&adminUsername)
			database.DB.Exec(
				"INSERT INTO dispute_messages (dispute_id, user_id, username, message, is_admin) VALUES ($1, $2, $3, '👑 Администратор подключился к спору и начал рассмотрение.', true)",
				disputeID, userID, adminUsername,
			)
		}
	}

	// Сообщения диспута
	msgRows, _ := database.DB.Query(
		"SELECT id, user_id, username, message, is_admin, created_at FROM dispute_messages WHERE dispute_id = $1 ORDER BY created_at ASC",
		disputeID,
	)
	if msgRows != nil {
		defer msgRows.Close()
	}

	type DisputeMessage struct {
		ID        int
		UserID    int
		Username  string
		Message   string
		IsAdmin   bool
		CreatedAt time.Time
	}

	var messages []DisputeMessage
	if msgRows != nil {
		for msgRows.Next() {
			var m DisputeMessage
			msgRows.Scan(&m.ID, &m.UserID, &m.Username, &m.Message, &m.IsAdmin, &m.CreatedAt)
			messages = append(messages, m)
		}
	}

	// Информация о заказе
	var order struct {
		ID      int
		Title   string
		Total   float64
		Status  string
		Booster string
	}
	database.DB.QueryRow(
		"SELECT o.id, o.title, o.total, o.status, u.username FROM orders o JOIN users u ON o.booster_id = u.id WHERE o.id = $1",
		dispute.OrderID,
	).Scan(&order.ID, &order.Title, &order.Total, &order.Status, &order.Booster)

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "dispute-detail",
		"Data": gin.H{
			"Dispute":  dispute,
			"Messages": messages,
			"Order":    order,
			"IsAdmin":  isAdminUser,
		},
	})
}

// Отправка сообщения в диспуте

func disputeSendMessage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	disputeID := c.Param("id")
	isAdminUser := isAdminSession(c)
	if !disputeParticipant(userID, disputeID) && !isAdminUser {
		c.String(http.StatusForbidden, "Доступ запрещён")
		return
	}

	var username string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&username)

	message := c.PostForm("message")

	if message != "" {
		database.DB.Exec(
			"INSERT INTO dispute_messages (dispute_id, user_id, username, message, is_admin) VALUES ($1,$2,$3,$4,$5)",
			disputeID, userID, username, message, isAdminUser,
		)
	}

	c.Redirect(302, "/dispute/"+disputeID)
}

// Решение админа по диспуту
