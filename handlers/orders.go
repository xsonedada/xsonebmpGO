package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"nexus-boost/database"
	"nexus-boost/models"
	"strconv"

	"github.com/gorilla/mux"
)

func OrderPageHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	vars := mux.Vars(r)
	orderID, _ := strconv.Atoi(vars["id"])

	var order models.Order
	err := database.DB.QueryRow(
		"SELECT id, user_id, boost_id, booster_id, title, game, booster, total, status, rated, created_at FROM orders WHERE id = ? AND user_id = ?",
		orderID, user.ID,
	).Scan(&order.ID, &order.UserID, &order.BoostID, &order.BoosterID, &order.Title, &order.Game, &order.Booster, &order.Total, &order.Status, &order.Rated, &order.CreatedAt)

	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Получаем сообщения
	msgRows, _ := database.DB.Query(
		"SELECT id, order_id, user_id, username, text, created_at FROM messages WHERE order_id = ? ORDER BY created_at ASC",
		orderID,
	)
	defer msgRows.Close()

	var messages []models.Message
	for msgRows.Next() {
		var msg models.Message
		msgRows.Scan(&msg.ID, &msg.OrderID, &msg.UserID, &msg.Username, &msg.Text, &msg.CreatedAt)
		messages = append(messages, msg)
	}
	if messages == nil {
		messages = []models.Message{}
	}

	RenderTemplate(w, "layout.html", models.PageData{
		Title:  fmt.Sprintf("Заказ #%d", order.ID),
		Active: "",
		User:   user,
		Data: map[string]interface{}{
			"Order":    order,
			"Messages": messages,
		},
	})
}

func SendMessageHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user == nil {
		http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
		return
	}

	vars := mux.Vars(r)
	orderID, _ := strconv.Atoi(vars["id"])
	text := r.FormValue("message")

	if text == "" {
		http.Redirect(w, r, fmt.Sprintf("/order/%d", orderID), http.StatusSeeOther)
		return
	}

	database.DB.Exec(
		"INSERT INTO messages (order_id, user_id, username, text) VALUES (?, ?, ?, ?)",
		orderID, user.ID, user.Username, text,
	)

	http.Redirect(w, r, fmt.Sprintf("/order/%d", orderID), http.StatusSeeOther)
}

func RateOrderHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user == nil {
		http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
		return
	}

	vars := mux.Vars(r)
	orderID, _ := strconv.Atoi(vars["id"])
	rating, _ := strconv.Atoi(r.FormValue("rating"))
	reviewText := r.FormValue("review")

	if rating < 1 || rating > 5 {
		http.Redirect(w, r, fmt.Sprintf("/order/%d?error=Неверный+рейтинг", orderID), http.StatusSeeOther)
		return
	}

	// Проверяем, что заказ принадлежит пользователю
	var order models.Order
	err := database.DB.QueryRow(
		"SELECT id, booster_id, rated FROM orders WHERE id = ? AND user_id = ?",
		orderID, user.ID,
	).Scan(&order.ID, &order.BoosterID, &order.Rated)

	if err != nil || order.Rated {
		http.Redirect(w, r, fmt.Sprintf("/order/%d", orderID), http.StatusSeeOther)
		return
	}

	// Добавляем отзыв
	database.DB.Exec(
		"INSERT INTO reviews (order_id, user_id, username, rating, text) VALUES (?, ?, ?, ?, ?)",
		orderID, user.ID, user.Username, rating, reviewText,
	)

	// Обновляем рейтинг бустера
	var currentRating float64
	var currentReviews int
	database.DB.QueryRow("SELECT rating, reviews FROM users WHERE id = ?", order.BoosterID).Scan(&currentRating, &currentReviews)

	newRating := (currentRating*float64(currentReviews) + float64(rating)) / float64(currentReviews+1)
	database.DB.Exec("UPDATE users SET rating = ?, reviews = ? WHERE id = ?", newRating, currentReviews+1, order.BoosterID)

	// Обновляем рейтинг буста
	var boostRating float64
	var boostReviews int
	var boostID int
	database.DB.QueryRow("SELECT boost_id FROM orders WHERE id = ?", orderID).Scan(&boostID)
	database.DB.QueryRow("SELECT rating, reviews FROM boosts WHERE id = ?", boostID).Scan(&boostRating, &boostReviews)

	newBoostRating := (boostRating*float64(boostReviews) + float64(rating)) / float64(boostReviews+1)
	database.DB.Exec("UPDATE boosts SET rating = ?, reviews = ? WHERE id = ?", newBoostRating, boostReviews+1, boostID)

	// Помечаем заказ как оцененный и выполненный
	database.DB.Exec("UPDATE orders SET rated = 1, status = 'completed' WHERE id = ?", orderID)

	http.Redirect(w, r, fmt.Sprintf("/order/%d?success=Спасибо+за+отзыв!", orderID), http.StatusSeeOther)
}

func BoosterProfileHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	vars := mux.Vars(r)
	boosterID, _ := strconv.Atoi(vars["id"])

	var booster models.User
	err := database.DB.QueryRow(
		"SELECT id, username, email, rating, reviews, orders, created_at FROM users WHERE id = ?",
		boosterID,
	).Scan(&booster.ID, &booster.Username, &booster.Email, &booster.Rating, &booster.Reviews, &booster.Orders, &booster.CreatedAt)

	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Получаем бусты бустера
	boostRows, _ := database.DB.Query(
		"SELECT id, game, title, description, price, rating, reviews FROM boosts WHERE user_id = ?",
		boosterID,
	)
	defer boostRows.Close()

	var boosts []models.Boost
	for boostRows.Next() {
		var b models.Boost
		boostRows.Scan(&b.ID, &b.Game, &b.Title, &b.Description, &b.Price, &b.Rating, &b.Reviews)
		boosts = append(boosts, b)
	}
	if boosts == nil {
		boosts = []models.Boost{}
	}

	// Получаем отзывы
	reviewRows, _ := database.DB.Query(
		"SELECT r.id, r.rating, r.text, r.username, r.created_at FROM reviews r JOIN orders o ON r.order_id = o.id WHERE o.booster_id = ? ORDER BY r.created_at DESC LIMIT 20",
		boosterID,
	)
	defer reviewRows.Close()

	var reviews []models.Review
	for reviewRows.Next() {
		var rev models.Review
		reviewRows.Scan(&rev.ID, &rev.Rating, &rev.Text, &rev.Username, &rev.CreatedAt)
		reviews = append(reviews, rev)
	}
	if reviews == nil {
		reviews = []models.Review{}
	}

	RenderTemplate(w, "layout.html", models.PageData{
		Title:  "Профиль бустера",
		Active: "booster-profile", // Должно совпадать с условием в layout.html
		User:   user,
		Data: map[string]interface{}{
			"Booster": booster,
			"Boosts":  boosts,
			"Reviews": reviews,
		},
	})
}

// API для сообщений
func GetMessagesHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	orderID, _ := strconv.Atoi(vars["id"])

	rows, _ := database.DB.Query(
		"SELECT id, order_id, user_id, username, text, created_at FROM messages WHERE order_id = ? ORDER BY created_at ASC",
		orderID,
	)
	defer rows.Close()

	var messages []models.Message
	for rows.Next() {
		var msg models.Message
		rows.Scan(&msg.ID, &msg.OrderID, &msg.UserID, &msg.Username, &msg.Text, &msg.CreatedAt)
		messages = append(messages, msg)
	}
	if messages == nil {
		messages = []models.Message{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}
