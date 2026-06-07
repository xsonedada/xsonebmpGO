package main

import (
	"encoding/json"
	"fmt"
	"nexus-boost/database"

	"github.com/gin-gonic/gin"
)

func cartPage(c *gin.Context) { render(c, "cart", nil) }

func getCart(c *gin.Context) {
	user := userFromContext(c)
	if user == nil {
		c.JSON(200, gin.H{"items": []CartItem{}, "total": 0})
		return
	}

	rows, _ := database.DB.Query("SELECT id, boost_id, title, price, game FROM cart_items WHERE user_id = $1", user.ID)
	if rows != nil {
		defer rows.Close()
	}

	var items []CartItem
	total := 0.0
	if rows != nil {
		for rows.Next() {
			var ci CartItem
			rows.Scan(&ci.ID, &ci.BoostID, &ci.Title, &ci.Price, &ci.Game)
			ci.Quantity = 1
			items = append(items, ci)
			total += ci.Price
		}
	}
	if items == nil {
		items = []CartItem{}
	}

	c.JSON(200, gin.H{"items": items, "total": total})
}

func addToCartAPI(c *gin.Context) {
	user := userFromContext(c)
	if user == nil {
		c.JSON(403, gin.H{"success": false, "message": "Требуется авторизация"})
		return
	}

	var req struct {
		BoostID int `json:"boost_id"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil || req.BoostID <= 0 {
		c.JSON(400, gin.H{"success": false, "message": "Неверные данные"})
		return
	}

	var title, game string
	var price float64
	var ownerID int
	err := database.DB.QueryRow(
		"SELECT title, game, price, user_id FROM boosts WHERE id = $1",
		req.BoostID,
	).Scan(&title, &game, &price, &ownerID)
	if err != nil {
		c.JSON(404, gin.H{"success": false, "message": "Товар не найден"})
		return
	}
	if ownerID == user.ID {
		c.JSON(400, gin.H{"success": false, "message": "Нельзя добавить в корзину свой товар"})
		return
	}
	if price <= 0 {
		c.JSON(400, gin.H{"success": false, "message": "Некорректная цена товара"})
		return
	}

	var count int
	err = database.DB.QueryRow(
		"SELECT COUNT(*) FROM cart_items WHERE user_id = $1 AND boost_id = $2",
		user.ID, req.BoostID,
	).Scan(&count)

	if count > 0 {
		var totalCount int
		database.DB.QueryRow("SELECT COUNT(*) FROM cart_items WHERE user_id = $1", user.ID).Scan(&totalCount)
		c.JSON(200, gin.H{"success": true, "cartCount": totalCount, "message": "Этот товар уже в корзине"})
		return
	}

	_, err = database.DB.Exec(
		"INSERT INTO cart_items (user_id, boost_id, title, price, game, booster_id) VALUES ($1,$2,$3,$4,$5,$6)",
		user.ID, req.BoostID, title, price, game, ownerID,
	)
	if err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}

	var totalCount int
	database.DB.QueryRow("SELECT COUNT(*) FROM cart_items WHERE user_id = $1", user.ID).Scan(&totalCount)

	c.JSON(200, gin.H{"success": true, "cartCount": totalCount, "message": "Добавлено в корзину!"})
}

func removeFromCart(c *gin.Context) {
	user := userFromContext(c)
	if user == nil {
		c.JSON(403, gin.H{"success": false, "message": "Требуется авторизация"})
		return
	}
	database.DB.Exec("DELETE FROM cart_items WHERE id=$1 AND user_id=$2", c.Param("id"), user.ID)
	c.JSON(200, gin.H{"success": true})
}

func clearCart(c *gin.Context) {
	user := userFromContext(c)
	if user == nil {
		c.JSON(403, gin.H{"success": false, "message": "Требуется авторизация"})
		return
	}
	database.DB.Exec("DELETE FROM cart_items WHERE user_id=$1", user.ID)
	c.JSON(200, gin.H{"success": true})
}

func checkout(c *gin.Context) {
	user := userFromContext(c)
	if user == nil {
		c.JSON(200, gin.H{"success": false, "message": "Требуется авторизация"})
		return
	}

	rows, _ := database.DB.Query(`
		SELECT ci.boost_id, b.title, b.price, b.game, b.user_id
		FROM cart_items ci
		JOIN boosts b ON b.id = ci.boost_id
		WHERE ci.user_id = $1`, user.ID)
	if rows != nil {
		defer rows.Close()
	}

	total := 0.0
	var items []struct {
		bid, bid2 int
		t, g      string
		p         float64
	}

	if rows != nil {
		for rows.Next() {
			var it struct {
				bid, bid2 int
				t, g      string
				p         float64
			}
			rows.Scan(&it.bid, &it.t, &it.p, &it.g, &it.bid2)
			items = append(items, it)
			total += it.p
		}
	}

	if len(items) == 0 {
		c.JSON(200, gin.H{"success": false, "message": "Корзина пуста"})
		return
	}

	// Транзакция для атомарного списания
	tx, err := database.DB.Begin()
	if err != nil {
		c.JSON(500, gin.H{"success": false, "message": "Ошибка сервера"})
		return
	}
	defer tx.Rollback()

	var bal float64
	tx.QueryRow("SELECT balance FROM users WHERE id = $1 FOR UPDATE", user.ID).Scan(&bal)

	if bal < total {
		c.JSON(200, gin.H{"success": false, "message": fmt.Sprintf("Недостаточно средств! Нужно %.0f ₽, у вас %.0f ₽", total, bal)})
		return
	}

	balanceBefore := bal
	var lastOrderID int

	for _, it := range items {
		err := tx.QueryRow(
			"INSERT INTO orders (user_id, boost_id, booster_id, title, game, total) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id",
			user.ID, it.bid, it.bid2, it.t, it.g, it.p,
		).Scan(&lastOrderID)

		if err == nil {
			tx.Exec("INSERT INTO escrow_transactions (order_id, buyer_id, seller_id, amount) VALUES ($1,$2,$3,$4)",
				lastOrderID, user.ID, it.bid2, it.p)

			// ... остальные вставки (сообщения, уведомления) тоже через tx ...
		}
	}

	tx.Exec("UPDATE users SET balance = balance - $1, orders = orders + $2 WHERE id = $3", total, len(items), user.ID)
	tx.Exec("DELETE FROM cart_items WHERE user_id = $1", user.ID)

	var newBal float64
	tx.QueryRow("SELECT balance FROM users WHERE id = $1", user.ID).Scan(&newBal)
	tx.Exec("INSERT INTO transaction_history (user_id, type, amount, balance_before, balance_after, description, order_id) VALUES ($1,'payment',$2,$3,$4,'Оплата заказа',$5)",
		user.ID, total, balanceBefore, newBal, lastOrderID)

	tx.Commit()

	go processReferralEarnings(user.ID, lastOrderID, total)

	c.JSON(200, gin.H{
		"success":    true,
		"message":    fmt.Sprintf("Заказ #%d оформлен! -%.0f ₽", lastOrderID, total),
		"newBalance": newBal,
		"orderID":    lastOrderID,
	})
}

func deleteBoost(c *gin.Context) {
	user := userFromContext(c)
	if user == nil {
		c.JSON(403, gin.H{"success": false, "message": "Требуется авторизация"})
		return
	}
	var oid int
	database.DB.QueryRow("SELECT user_id FROM boosts WHERE id=$1", c.Param("id")).Scan(&oid)
	if oid == user.ID {
		database.DB.Exec("DELETE FROM boosts WHERE id=$1", c.Param("id"))
		c.JSON(200, gin.H{"success": true})
	} else {
		c.JSON(200, gin.H{"success": false})
	}
}
