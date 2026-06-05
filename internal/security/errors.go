package security

import "errors"

var (
    ErrEmptyInput    = errors.New("поле не может быть пустым")
    ErrTooLong       = errors.New("превышена максимальная длина")
    ErrInvalidFormat = errors.New("неверный формат данных")
    ErrInvalidPrice  = errors.New("неверная цена")
    ErrInvalidID     = errors.New("неверный ID")
    ErrAccessDenied  = errors.New("доступ запрещен")
    ErrCSRFInvalid   = errors.New("CSRF токен недействителен")
    ErrRateLimit     = errors.New("слишком много запросов")
    ErrSessionExpired = errors.New("сессия истекла")
)