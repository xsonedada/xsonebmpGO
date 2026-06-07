package main

import (
	"log"
	"nexus-boost/database"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/sessions"
)

func initPreparedStatements() {
	var err error
	stmtGetUser, err = database.DB.Prepare(`
        SELECT u.id, u.username, u.email, u.balance, u.level, u.orders,
               COALESCE((SELECT COUNT(*) FROM orders WHERE booster_id = u.id), 0) as seller_orders
        FROM users u
        WHERE u.id = $1
          AND NOT EXISTS (SELECT 1 FROM banned_users WHERE user_id = u.id)
          AND (
            $2 = '' OR NOT EXISTS (
              SELECT 1 FROM user_sessions WHERE session_token = $2 AND revoked = true
            )
          )
    `)
	if err != nil {
		log.Printf("❌ Ошибка подготовки stmtGetUser: %v", err)
	}
}

func userFromContext(c *gin.Context) *User {
	u, ok := c.Get("user")
	if !ok {
		return nil
	}
	user, ok := u.(*User)
	if !ok || user == nil {
		return nil
	}
	return user
}

func getUser(c *gin.Context) *User {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		return nil
	}

	sessionUUID, _ := session.Values["session_uuid"].(string)

	var u User
	if stmtGetUser != nil {
		err := stmtGetUser.QueryRow(userID, sessionUUID).Scan(
			&u.ID, &u.Username, &u.Email, &u.Balance, &u.Level, &u.Orders, &u.SellerOrders,
		)
		if err != nil {
			if sessionUUID != "" {
				var revoked bool
				if database.DB.QueryRow(
					"SELECT revoked FROM user_sessions WHERE session_token = $1", sessionUUID,
				).Scan(&revoked) == nil && revoked {
					session.Values = make(map[interface{}]interface{})
					session.Save(c.Request, c.Writer)
				}
			}
			return nil
		}
	} else {
		err := database.DB.QueryRow(`
            SELECT u.id, u.username, u.email, u.balance, u.level, u.orders,
                   COALESCE((SELECT COUNT(*) FROM orders WHERE booster_id = u.id), 0)
            FROM users u
            WHERE u.id = $1
              AND NOT EXISTS (SELECT 1 FROM banned_users WHERE user_id = $1)
              AND (
                $2 = '' OR NOT EXISTS (
                  SELECT 1 FROM user_sessions WHERE session_token = $2 AND revoked = true
                )
              )
        `, userID, sessionUUID).Scan(&u.ID, &u.Username, &u.Email, &u.Balance, &u.Level, &u.Orders, &u.SellerOrders)
		if err != nil {
			return nil
		}
	}

	return &u
}

func setUser(c *gin.Context, userID int, username string) {
	session, _ := store.Get(c.Request, "xsonebmp-session")
	session.Values["user_id"] = userID
	session.Values["username"] = username
	session.Save(c.Request, c.Writer)
}

func regenerateSession(c *gin.Context) (*sessions.Session, error) {
	old, err := store.Get(c.Request, "xsonebmp-session")
	if err != nil {
		return nil, err
	}

	newValues := make(map[interface{}]interface{})
	for k, v := range old.Values {
		newValues[k] = v
	}

	old.Options.MaxAge = -1
	if err := old.Save(c.Request, c.Writer); err != nil {
		return nil, err
	}

	newSess, err := store.New(c.Request, "xsonebmp-session")
	if err != nil {
		return nil, err
	}
	newSess.Values = newValues
	if err := newSess.Save(c.Request, c.Writer); err != nil {
		return nil, err
	}
	return newSess, nil
}
