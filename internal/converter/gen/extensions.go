package gen

import (
	"time"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// Extend functions for type conversions

func ConvertTime(t time.Time) time.Time {
	return t
}

func ConvertNullIntToInt(i *int) int {
	if i != nil {
		return *i
	}
	return 0
}

// ConvertSmallIntToInt carries a smallint column into the API's int.
//
// A GraphQL `Int!` binds to Go int, and the width belongs to the database (migration
// 120 chose smallint for a 0-3 value), so the two have to meet somewhere -- here rather
// than by widening the column to an int4 nobody needs.
func ConvertSmallIntToInt(i int16) int {
	return int(i)
}

func ConvertNotificationType(t queries.NotificationType) models.NotificationEnum {
	return models.NotificationEnum(t)
}
