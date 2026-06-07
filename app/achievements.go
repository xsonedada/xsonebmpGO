package main

import (
	"net/http"
	"nexus-boost/database"

	"github.com/gin-gonic/gin"
)

func getSellerLevel(orders int, rating float64) (string, string, int) {
	var levelName, color string
	var level int

	// Если рейтинг 0 - считаем как 5.0 (начальный)
	if rating == 0 {
		rating = 5.0
	}

	rows, _ := database.DB.Query(
		"SELECT level, name, color FROM seller_levels WHERE min_orders <= $1 AND min_rating <= $2 ORDER BY level DESC LIMIT 1",
		orders, rating,
	)
	if rows != nil {
		defer rows.Close()
		if rows.Next() {
			rows.Scan(&level, &levelName, &color)
		}
	}

	if level == 0 {
		level = 1
		levelName = "Новичок"
		color = "#10b981"
	}

	return levelName, color, level
}

// Проверить достижения

func checkAchievements(userID int) {
	// Считаем ПРОДАЖИ (где пользователь - booster)
	var salesCount int
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE booster_id = $1 AND status = 'completed'", userID).Scan(&salesCount)

	// Рейтинг продавца
	var rating float64
	database.DB.QueryRow("SELECT COALESCE(rating, 0) FROM users WHERE id = $1", userID).Scan(&rating)

	// Заработок с продаж
	var earned float64
	database.DB.QueryRow("SELECT COALESCE(SUM(total), 0) FROM orders WHERE booster_id = $1 AND status = 'completed'", userID).Scan(&earned)

	rows, _ := database.DB.Query("SELECT id, condition_field, condition_value FROM achievements")
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var id int
			var field string
			var value int
			rows.Scan(&id, &field, &value)

			var achieved bool
			switch field {
			case "orders":
				achieved = salesCount >= value // ← ИСПРАВЛЕНО: считаем продажи
			case "rating":
				achieved = int(rating*10) >= value // 4.5 → 45
			case "earned":
				achieved = int(earned) >= value
			}

			if achieved {
				database.DB.Exec("INSERT INTO user_achievements (user_id, achievement_id) VALUES ($1,$2) ON CONFLICT DO NOTHING", userID, id)
			}
		}
	}
}

// Страница достижений в профиле

func achievementsPage(c *gin.Context) {
	u, exists := c.Get("user")
	if !exists || u == nil {
		c.Redirect(302, "/login")
		return
	}

	user, ok := u.(*User)
	if !ok {
		c.Redirect(302, "/login")
		return
	}

	// Обновляем достижения на основе ПРОДАЖ
	checkAchievements(user.ID)

	// Считаем продажи
	var salesCount int
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE booster_id = $1 AND status = 'completed'", user.ID).Scan(&salesCount)

	// Уровень продавца
	levelName, levelColor, _ := getSellerLevel(salesCount, user.Rating)

	// Все достижения
	rows, _ := database.DB.Query(`
        SELECT a.id, a.name, a.description, a.icon, 
               CASE WHEN ua.user_id IS NOT NULL THEN true ELSE false END as achieved
        FROM achievements a 
        LEFT JOIN user_achievements ua ON a.id = ua.achievement_id AND ua.user_id = $1
        ORDER BY a.id
    `, user.ID)
	if rows != nil {
		defer rows.Close()
	}

	type Achievement struct {
		ID          int
		Name        string
		Description string
		Icon        string
		Achieved    bool
	}

	var achievements []Achievement
	if rows != nil {
		for rows.Next() {
			var a Achievement
			rows.Scan(&a.ID, &a.Name, &a.Description, &a.Icon, &a.Achieved)
			achievements = append(achievements, a)
		}
	}

	// Следующий уровень
	var nextLevelName string
	var ordersNeeded int
	database.DB.QueryRow("SELECT name, min_orders FROM seller_levels WHERE min_orders > $1 ORDER BY level ASC LIMIT 1", salesCount).
		Scan(&nextLevelName, &ordersNeeded)

	layoutHTML(c, http.StatusOK, gin.H{
		"User":   user,
		"Active": "achievements",
		"Data": gin.H{
			"LevelName":    levelName,
			"LevelColor":   levelColor,
			"Achievements": achievements,
			"NextLevel":    nextLevelName,
			"OrdersNeeded": ordersNeeded - salesCount,
		},
	})
}

func giveBadge(userID int, badgeType, badgeName, badgeIcon, badgeColor string) {
	database.DB.Exec("INSERT INTO user_badges (user_id, badge_type, badge_name, badge_icon, badge_color) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (user_id, badge_type) DO NOTHING",
		userID, badgeType, badgeName, badgeIcon, badgeColor)
}

// Получить бейджи пользователя

func getUserBadges(userID int) []gin.H {
	rows, _ := database.DB.Query("SELECT badge_name, badge_icon, badge_color FROM user_badges WHERE user_id = $1", userID)
	if rows != nil {
		defer rows.Close()
	}

	var badges []gin.H
	if rows != nil {
		for rows.Next() {
			var name, icon, color string
			rows.Scan(&name, &icon, &color)
			badges = append(badges, gin.H{"name": name, "icon": icon, "color": color})
		}
	}
	return badges
}

// API - верификация продавца админом

func checkAndGiveBadges(userID int) {
	var orders int
	database.DB.QueryRow("SELECT COUNT(*) FROM orders WHERE booster_id = $1 AND status = 'completed'", userID).Scan(&orders)

	var rating float64
	database.DB.QueryRow("SELECT COALESCE(rating, 0) FROM users WHERE id = $1", userID).Scan(&rating)

	if orders >= 10 {
		giveBadge(userID, "pro", "Опытный продавец", "🌟", "#fbbf24")
	}
	if orders >= 50 {
		giveBadge(userID, "master", "Мастер продаж", "👑", "#ff6b6b")
	}
	if rating >= 4.8 {
		giveBadge(userID, "top-rated", "Топ-рейтинг", "⭐", "#f59e0b")
	}
}

// Страница заявки на верификацию
