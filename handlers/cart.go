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

func CartPageHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	RenderTemplate(w, "layout.html", models.PageData{
		Title:  "Корзина",
		Active: "cart",
		User:   user,
	})
}

func GetCartHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []models.CartItem{},
			"total": 0.0,
		})
		return
	}

	rows, err := database.DB.Query(
		"SELECT id, user_id, boost_id, title, price, game, booster, quantity FROM cart_items WHERE user_id = ?",
		user.ID,
	)
	if err != nil {
		http.Error(w, "Ошибка получения корзины", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var items []models.CartItem
	total := 0.0

	for rows.Next() {
		var item models.CartItem
		rows.Scan(&item.ID, &item.UserID, &item.BoostID, &item.Title, &item.Price, &item.Game, &item.Booster, &item.Quantity)
		items = append(items, item)
		total += item.Price * float64(item.Quantity)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"items": items,
		"total": total,
	})
}

func AddToCartHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user == nil {
		http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
		return
	}

	var item models.CartItem
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		http.Error(w, "Неверный запрос", http.StatusBadRequest)
		return
	}

	// Проверяем, есть ли уже такой товар в корзине
	var existingID int
	var existingQuantity int
	err := database.DB.QueryRow(
		"SELECT id, quantity FROM cart_items WHERE user_id = ? AND boost_id = ?",
		user.ID, item.BoostID,
	).Scan(&existingID, &existingQuantity)

	if err == nil {
		// Обновляем количество
		database.DB.Exec(
			"UPDATE cart_items SET quantity = ? WHERE id = ?",
			existingQuantity+1, existingID,
		)
	} else {
		// Добавляем новый товар
		database.DB.Exec(
			"INSERT INTO cart_items (user_id, boost_id, title, price, game, booster, quantity) VALUES (?, ?, ?, ?, ?, ?, 1)",
			user.ID, item.BoostID, item.Title, item.Price, item.Game, item.Booster,
		)
	}

	// Получаем количество товаров в корзине
	var count int
	database.DB.QueryRow("SELECT COUNT(*) FROM cart_items WHERE user_id = ?", user.ID).Scan(&count)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"cartCount": count,
		"message":   "Товар добавлен в корзину",
	})
}

func RemoveFromCartHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user == nil {
		http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
		return
	}

	vars := mux.Vars(r)
	itemID, _ := strconv.Atoi(vars["id"])

	database.DB.Exec("DELETE FROM cart_items WHERE id = ? AND user_id = ?", itemID, user.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Товар удален",
	})
}

func ClearCartHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user == nil {
		http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
		return
	}

	database.DB.Exec("DELETE FROM cart_items WHERE user_id = ?", user.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Корзина очищена",
	})
}

func CheckoutHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user == nil {
		http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
		return
	}

	rows, err := database.DB.Query(
		"SELECT id, boost_id, title, price, game, booster, booster_id FROM cart_items WHERE user_id = ?",
		user.ID,
	)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Ошибка получения корзины",
		})
		return
	}
	defer rows.Close()

	total := 0.0
	var items []struct {
		ID        int
		BoostID   int
		Title     string
		Price     float64
		Game      string
		Booster   string
		BoosterID int
	}

	for rows.Next() {
		var item struct {
			ID        int
			BoostID   int
			Title     string
			Price     float64
			Game      string
			Booster   string
			BoosterID int
		}
		rows.Scan(&item.ID, &item.BoostID, &item.Title, &item.Price, &item.Game, &item.Booster, &item.BoosterID)
		items = append(items, item)
		total += item.Price
	}

	if len(items) == 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "Корзина пуста",
		})
		return
	}

	var balance float64
	database.DB.QueryRow("SELECT balance FROM users WHERE id = ?", user.ID).Scan(&balance)

	if balance < total {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("Недостаточно средств! Нужно %.2f ₽, доступно %.2f ₽", total, balance),
		})
		return
	}

	tx, _ := database.DB.Begin()

	for _, item := range items {
		tx.Exec(
			"INSERT INTO orders (user_id, boost_id, booster_id, title, game, booster, total, status) VALUES (?, ?, ?, ?, ?, ?, ?, 'processing')",
			user.ID, item.BoostID, item.BoosterID, item.Title, item.Game, item.Booster, item.Price,
		)
	}

	tx.Exec("UPDATE users SET balance = balance - ?, orders = orders + ? WHERE id = ?", total, len(items), user.ID)
	tx.Exec("DELETE FROM cart_items WHERE user_id = ?", user.ID)

	tx.Commit()

	database.DB.QueryRow("SELECT balance FROM users WHERE id = ?", user.ID).Scan(&balance)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"message":    fmt.Sprintf("Заказ оформлен! Списано %.2f ₽", total),
		"newBalance": balance,
	})
}
