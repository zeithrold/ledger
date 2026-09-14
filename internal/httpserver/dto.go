package httpserver

import (
	"github.com/zeithrold/ledger/internal/apiv1"
	"github.com/zeithrold/ledger/internal/identity"
)

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func userDTO(v identity.User) apiv1.User {
	return apiv1.User{Id: v.ID, DisplayName: v.DisplayName, Status: apiv1.UserStatus(v.Status)}
}

func bookDTO(v identity.Book) apiv1.Book {
	return apiv1.Book{Id: v.ID, TenantId: v.TenantID, Name: v.Name, BaseCurrency: v.BaseCurrency}
}

func preferencesDTO(v identity.Preferences) apiv1.Preferences {
	return apiv1.Preferences{Locale: v.Locale, Timezone: v.Timezone, Theme: apiv1.PreferencesTheme(v.Theme)}
}

func contextDTO(v identity.Context) apiv1.UserContext {
	return apiv1.UserContext{User: userDTO(v.User), InstanceRole: apiv1.UserContextInstanceRole(v.InstanceRole), Tenant: apiv1.Tenant{Id: v.Tenant.ID, Name: v.Tenant.Name, Role: apiv1.TenantRole(v.Tenant.Role)}, DefaultBook: bookDTO(v.DefaultBook), Preferences: preferencesDTO(v.Preferences)}
}

func userPageDTO(v identity.UserPage) apiv1.UserPage {
	users := make([]apiv1.User, len(v.Users))
	for i, u := range v.Users {
		users[i] = userDTO(u)
	}
	return apiv1.UserPage{Users: users, NextCursor: v.NextCursor}
}
