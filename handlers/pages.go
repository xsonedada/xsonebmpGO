package handlers

import (
	"html/template"
	"log"
	"net/http"
	"nexus-boost/database"
	"nexus-boost/models"
)

var templates *template.Template

func InitTemplates(t *template.Template) {
	templates = t
}

func RenderTemplate(w http.ResponseWriter, tmpl string, data models.PageData) {
	if data.User == nil {
		// Пробуем получить пользователя из сессии
		session, _ := Store.Get(&http.Request{}, "nexus-session")
		if userID, ok := session.Values["user_id"].(int); ok {
			var user models.User
			err := database.DB.QueryRow(
				"SELECT id, username, email, balance, level, orders FROM users WHERE id = ?",
				userID,
			).Scan(&user.ID, &user.Username, &user.Email, &user.Balance, &user.Level, &user.Orders)
			if err == nil {
				data.User = &user
			}
		}
	}

	err := templates.ExecuteTemplate(w, tmpl, data)
	if err != nil {
		log.Printf("Ошибка рендеринга: %v", err)
	}
}
