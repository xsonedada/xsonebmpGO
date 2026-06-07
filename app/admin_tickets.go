package main

import (
	"net/http"
	"nexus-boost/database"
	"time"

	"github.com/gin-gonic/gin"
)

func adminTicketsPage(c *gin.Context) {
	filter := c.Query("status")

	query := "SELECT id, user_id, category, subject, status, priority, created_at FROM tickets"

	if filter == "open" {
		query += " WHERE status = 'open'"
	} else if filter == "in_progress" {
		query += " WHERE status = 'in_progress'"
	} else if filter == "closed" {
		query += " WHERE status = 'closed'"
	}

	query += " ORDER BY CASE status WHEN 'open' THEN 1 WHEN 'in_progress' THEN 2 WHEN 'closed' THEN 3 END, CASE priority WHEN 'urgent' THEN 1 WHEN 'high' THEN 2 WHEN 'normal' THEN 3 WHEN 'low' THEN 4 END, created_at DESC"

	rows, _ := database.DB.Query(query)
	if rows != nil {
		defer rows.Close()
	}

	user, _ := c.Get("user")

	type TicketRow struct {
		ID        int
		UserID    int
		Category  string
		Subject   string
		Status    string
		Priority  string
		CreatedAt time.Time
	}

	var tickets []TicketRow
	if rows != nil {
		for rows.Next() {
			var t TicketRow
			rows.Scan(&t.ID, &t.UserID, &t.Category, &t.Subject, &t.Status, &t.Priority, &t.CreatedAt)
			tickets = append(tickets, t)
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-tickets",
		"User":   user,
		"Data":   gin.H{"Tickets": tickets, "Filter": filter},
	})
}

func adminTicketDetail(c *gin.Context) {
	ticketID := c.Param("id")
	user, _ := c.Get("user")
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
		"SELECT t.id, t.user_id, t.category, t.subject, t.description, t.status, t.priority, t.created_at FROM tickets t WHERE t.id = $1",
		ticketID,
	).Scan(&ticket.ID, &ticket.UserID, &ticket.Category, &ticket.Subject, &ticket.Description, &ticket.Status, &ticket.Priority, &ticket.CreatedAt)

	// Сообщения
	msgRows, _ := database.DB.Query(
		"SELECT id, username, message, is_admin, created_at FROM ticket_messages WHERE ticket_id = $1 ORDER BY created_at ASC",
		ticketID,
	)
	if msgRows != nil {
		defer msgRows.Close()
	}

	var messages []gin.H
	if msgRows != nil {
		for msgRows.Next() {
			var id int
			var username, message string
			var isAdmin bool
			var createdAt time.Time
			msgRows.Scan(&id, &username, &message, &isAdmin, &createdAt)
			messages = append(messages, gin.H{"id": id, "username": username, "message": message, "is_admin": isAdmin, "created_at": createdAt})
		}
	}

	layoutHTML(c, http.StatusOK, gin.H{
		"Active": "admin-ticket-detail",
		"User":   user,
		"Data":   gin.H{"Ticket": ticket, "Messages": messages},
	})
}

func adminTicketMessage(c *gin.Context) {
	ticketID := c.Param("id")
	message := c.PostForm("message")

	if message != "" {
		database.DB.Exec(
			"INSERT INTO ticket_messages (ticket_id, user_id, username, message, is_admin) VALUES ($1, 0, 'Поддержка XSoneBMP', $2, true)",
			ticketID, message,
		)
		database.DB.Exec("UPDATE tickets SET status = 'in_progress', updated_at = NOW() WHERE id = $1 AND status = 'open'", ticketID)
	}

	c.Redirect(302, "/admin/ticket/"+ticketID)
}

func adminTicketStatus(c *gin.Context) {
	ticketID := c.Param("id")
	status := c.PostForm("status")

	database.DB.Exec("UPDATE tickets SET status = $1, updated_at = NOW() WHERE id = $2", status, ticketID)
	c.Redirect(302, "/admin/ticket/"+ticketID)
}

// Страница эскроу
