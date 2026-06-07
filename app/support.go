package main

import (
	"fmt"
	"net/http"
	"nexus-boost/database"
	"time"

	"github.com/gin-gonic/gin"
)

func supportPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	rows, _ := database.DB.Query(
		"SELECT id, category, subject, status, priority, created_at, updated_at FROM tickets WHERE user_id = $1 ORDER BY updated_at DESC",
		userID,
	)
	if rows != nil {
		defer rows.Close()
	}

	type Ticket struct {
		ID        int
		Category  string
		Subject   string
		Status    string
		Priority  string
		CreatedAt time.Time
		UpdatedAt time.Time
	}

	var tickets []Ticket
	if rows != nil {
		for rows.Next() {
			var t Ticket
			rows.Scan(&t.ID, &t.Category, &t.Subject, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt)
			tickets = append(tickets, t)
		}
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "support",
		"Data":   gin.H{"Tickets": tickets},
	})
}

// Создание тикета

func createTicketPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "support-create",
	})
}

func createTicket(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	category := c.PostForm("category")
	subject := c.PostForm("subject")
	description := c.PostForm("description")
	priority := c.PostForm("priority")

	var ticketID int
	database.DB.QueryRow(
		"INSERT INTO tickets (user_id, category, subject, description, priority) VALUES ($1,$2,$3,$4,$5) RETURNING id",
		userID, category, subject, description, priority,
	).Scan(&ticketID)

	c.Redirect(302, "/support/ticket/"+fmt.Sprintf("%d", ticketID))
}

// Детали тикета

func ticketDetailPage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"]
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	ticketID := c.Param("id")

	var ticket struct {
		ID          int
		UserID      int
		Category    string
		Subject     string
		Description string
		Status      string
		Priority    string
		CreatedAt   time.Time
	}

	database.DB.QueryRow(
		"SELECT id, user_id, category, subject, description, status, priority, created_at FROM tickets WHERE id = $1 AND user_id = $2",
		ticketID, userID,
	).Scan(&ticket.ID, &ticket.UserID, &ticket.Category, &ticket.Subject, &ticket.Description, &ticket.Status, &ticket.Priority, &ticket.CreatedAt)

	// Сообщения
	msgRows, _ := database.DB.Query(
		"SELECT id, username, message, is_admin, created_at FROM ticket_messages WHERE ticket_id = $1 ORDER BY created_at ASC",
		ticketID,
	)
	if msgRows != nil {
		defer msgRows.Close()
	}

	type TicketMessage struct {
		ID        int
		Username  string
		Message   string
		IsAdmin   bool
		CreatedAt time.Time
	}

	var messages []TicketMessage
	if msgRows != nil {
		for msgRows.Next() {
			var m TicketMessage
			msgRows.Scan(&m.ID, &m.Username, &m.Message, &m.IsAdmin, &m.CreatedAt)
			messages = append(messages, m)
		}
	}

	var user User
	database.DB.QueryRow("SELECT id, username FROM users WHERE id = $1", userID).Scan(&user.ID, &user.Username)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   &user,
		"Active": "support-ticket",
		"Data":   gin.H{"Ticket": ticket, "Messages": messages},
	})
}

// Отправка сообщения в тикет

func ticketSendMessage(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	ticketID := c.Param("id")

	var ticketOwner int
	err := database.DB.QueryRow("SELECT user_id FROM tickets WHERE id = $1", ticketID).Scan(&ticketOwner)
	if err != nil || ticketOwner != userID {
		c.String(http.StatusForbidden, "Доступ запрещён")
		return
	}

	message := c.PostForm("message")

	var username string
	database.DB.QueryRow("SELECT username FROM users WHERE id = $1", userID).Scan(&username)

	if message != "" {
		database.DB.Exec(
			"INSERT INTO ticket_messages (ticket_id, user_id, username, message) VALUES ($1,$2,$3,$4)",
			ticketID, userID, username, message,
		)
		database.DB.Exec("UPDATE tickets SET updated_at = NOW() WHERE id = $1", ticketID)
	}

	c.Redirect(302, "/support/ticket/"+ticketID)
}

// Закрытие тикета

func closeTicket(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	ticketID := c.Param("id")
	database.DB.Exec("UPDATE tickets SET status = 'closed', updated_at = NOW() WHERE id = $1 AND user_id = $2", ticketID, userID)

	c.Redirect(302, "/support/ticket/"+ticketID)
}

// ============ АДМИНКА ТИКЕТЫ ============
