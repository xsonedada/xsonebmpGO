package main

import (
	"encoding/json"
	"fmt"
	"nexus-boost/database"

	"github.com/gin-gonic/gin"
)

func checkPromoCode(c *gin.Context) {
	var request struct {
		Code   string  `json:"code"`
		Amount float64 `json:"amount"`
	}
	json.NewDecoder(c.Request.Body).Decode(&request)

	if request.Code == "" {
		c.JSON(400, gin.H{"success": false, "message": "Введите промокод"})
		return
	}

	var promo struct {
		ID              int
		DiscountPercent int
		MaxUses         int
		UsedCount       int
		MinOrderAmount  float64
		IsActive        bool
	}

	err := database.DB.QueryRow(
		"SELECT id, discount_percent, max_uses, used_count, min_order_amount, is_active FROM promocodes WHERE code = $1",
		request.Code,
	).Scan(&promo.ID, &promo.DiscountPercent, &promo.MaxUses, &promo.UsedCount, &promo.MinOrderAmount, &promo.IsActive)

	if err != nil {
		c.JSON(404, gin.H{"success": false, "message": "Промокод не найден"})
		return
	}

	if !promo.IsActive {
		c.JSON(400, gin.H{"success": false, "message": "Промокод недействителен"})
		return
	}

	if promo.MaxUses > 0 && promo.UsedCount >= promo.MaxUses {
		c.JSON(400, gin.H{"success": false, "message": "Лимит использования исчерпан"})
		return
	}

	if request.Amount < promo.MinOrderAmount {
		c.JSON(400, gin.H{"success": false, "message": fmt.Sprintf("Минимальная сумма заказа: %.0f ₽", promo.MinOrderAmount)})
		return
	}

	discountAmount := request.Amount * float64(promo.DiscountPercent) / 100

	c.JSON(200, gin.H{
		"success":          true,
		"message":          fmt.Sprintf("Промокод применен! Скидка %d%%", promo.DiscountPercent),
		"discount_percent": promo.DiscountPercent,
		"discount_amount":  discountAmount,
		"final_amount":     request.Amount - discountAmount,
	})
}

// Страница промокодов в админке

func checkFirstOrderDiscount(userID int) (bool, int) {
	var orderCount int
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE user_id = $1", userID).Scan(&orderCount)

	if orderCount == 0 {
		var discountPercent int
		var isActive bool
		database.DB.QueryRow("SELECT discount_percent, is_active FROM first_order_discount LIMIT 1").Scan(&discountPercent, &isActive)

		if isActive {
			return true, discountPercent
		}
	}

	return false, 0
}

// Проверка сезонных акций

func checkSeasonalSale() (bool, string, int) {
	var sale struct {
		Name            string
		DiscountPercent int
	}

	err := database.DB.QueryRow(
		"SELECT name, discount_percent FROM sales WHERE is_active = true AND NOW() BETWEEN start_date AND end_date ORDER BY discount_percent DESC LIMIT 1",
	).Scan(&sale.Name, &sale.DiscountPercent)

	if err == nil {
		return true, sale.Name, sale.DiscountPercent
	}

	return false, "", 0
}

// API для получения доступных скидок

func getAvailableDiscounts(c *gin.Context) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, _ := session.Values["user_id"]

	var discounts []gin.H

	// Скидка на первый заказ
	if userID != nil {
		hasFirstDiscount, firstDiscount := checkFirstOrderDiscount(userID.(int))
		if hasFirstDiscount {
			discounts = append(discounts, gin.H{
				"type":    "first_order",
				"name":    "Скидка на первый заказ",
				"percent": firstDiscount,
				"code":    "FIRST",
			})
		}
	}

	// Сезонная акция
	hasSale, saleName, saleDiscount := checkSeasonalSale()
	if hasSale {
		discounts = append(discounts, gin.H{
			"type":    "seasonal",
			"name":    saleName,
			"percent": saleDiscount,
			"code":    "SEASON",
		})
	}

	c.JSON(200, gin.H{"discounts": discounts})
}

func applyDiscount(c *gin.Context) {
	var request struct {
		Code   string  `json:"code"`
		Amount float64 `json:"amount"`
	}
	json.NewDecoder(c.Request.Body).Decode(&request)

	switch request.Code {
	case "FIRST":
		session, _ := store.Get(c.Request, "xsonebmp-session")
		userID, _ := session.Values["user_id"]

		hasDiscount, percent := checkFirstOrderDiscount(userID.(int))
		if hasDiscount {
			discountAmount := request.Amount * float64(percent) / 100
			c.JSON(200, gin.H{
				"success":          true,
				"message":          fmt.Sprintf("Скидка на первый заказ %d%% применена!", percent),
				"discount_percent": percent,
				"discount_amount":  discountAmount,
				"final_amount":     request.Amount - discountAmount,
			})
			return
		}

	case "SEASON":
		hasSale, saleName, percent := checkSeasonalSale()
		if hasSale {
			discountAmount := request.Amount * float64(percent) / 100
			c.JSON(200, gin.H{
				"success":          true,
				"message":          fmt.Sprintf("%s: скидка %d%% применена!", saleName, percent),
				"discount_percent": percent,
				"discount_amount":  discountAmount,
				"final_amount":     request.Amount - discountAmount,
			})
			return
		}
	}

	c.JSON(400, gin.H{"success": false, "message": "Скидка недоступна"})
}
