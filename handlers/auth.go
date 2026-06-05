package handlers

import (
	"net/http"
	"nexus-boost/database"
	"nexus-boost/models"

	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"
)

var Store = sessions.NewCookieStore([]byte("nexus-boost-secret-key-2024-very-secure"))

func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		RenderTemplate(w, "layout.html", models.PageData{
			Title:  "Регистрация",
			Active: "register",
		})
		return
	}

	if r.Method == "POST" {
		username := r.FormValue("username")
		email := r.FormValue("email")
		password := r.FormValue("password")

		if username == "" || email == "" || password == "" {
			RenderTemplate(w, "layout.html", models.PageData{
				Title: "Регистрация",
				Error: "Все поля обязательны для заполнения",
			})
			return
		}

		if len(password) < 6 {
			RenderTemplate(w, "layout.html", models.PageData{
				Title: "Регистрация",
				Error: "Пароль должен быть минимум 6 символов",
			})
			return
		}

		// Хеширование пароля
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			RenderTemplate(w, "layout.html", models.PageData{
				Title: "Регистрация",
				Error: "Ошибка обработки пароля",
			})
			return
		}

		// Добавление пользователя
		result, err := database.DB.Exec(
			"INSERT INTO users (username, email, password, balance) VALUES (?, ?, ?, 10000.00)",
			username, email, string(hashedPassword),
		)
		if err != nil {
			RenderTemplate(w, "layout.html", models.PageData{
				Title: "Регистрация",
				Error: "Пользователь с таким именем или email уже существует",
			})
			return
		}

		userID, _ := result.LastInsertId()

		// Создание сессии
		session, _ := Store.Get(r, "nexus-session")
		session.Values["user_id"] = int(userID)
		session.Values["username"] = username
		session.Save(r, w)

		http.Redirect(w, r, "/profile", http.StatusSeeOther)
		return
	}
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		RenderTemplate(w, "layout.html", models.PageData{
			Title:  "Вход",
			Active: "login",
		})
		return
	}

	if r.Method == "POST" {
		username := r.FormValue("username")
		password := r.FormValue("password")

		if username == "" || password == "" {
			RenderTemplate(w, "layout.html", models.PageData{
				Title: "Вход",
				Error: "Введите имя пользователя и пароль",
			})
			return
		}

		var user models.User
		err := database.DB.QueryRow(
			"SELECT id, username, email, password, balance, level, orders FROM users WHERE username = ?",
			username,
		).Scan(&user.ID, &user.Username, &user.Email, &user.Password, &user.Balance, &user.Level, &user.Orders)

		if err != nil {
			RenderTemplate(w, "layout.html", models.PageData{
				Title: "Вход",
				Error: "Неверное имя пользователя или пароль",
			})
			return
		}

		// Проверка пароля
		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
			RenderTemplate(w, "layout.html", models.PageData{
				Title: "Вход",
				Error: "Неверное имя пользователя или пароль",
			})
			return
		}

		// Создание сессии
		session, _ := Store.Get(r, "nexus-session")
		session.Values["user_id"] = user.ID
		session.Values["username"] = user.Username
		session.Save(r, w)

		http.Redirect(w, r, "/profile", http.StatusSeeOther)
		return
	}
}

func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	session, _ := Store.Get(r, "nexus-session")
	session.Values["user_id"] = nil
	session.Values["username"] = nil
	session.Save(r, w)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// GetUser возвращает пользователя из сессии
func GetUser(r *http.Request) *models.User {
	session, _ := Store.Get(r, "nexus-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok || userID == 0 {
		return nil
	}

	var user models.User
	err := database.DB.QueryRow(
		"SELECT id, username, email, balance, level, orders FROM users WHERE id = ?",
		userID,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Balance, &user.Level, &user.Orders)

	if err != nil {
		return nil
	}

	return &user
}
