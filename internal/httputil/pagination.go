package httputil

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func ParsePagination(c *gin.Context) (limit, offset int, ok bool) {
	limit, offset = 20, 0
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 100"})
			return 0, 0, false
		}
		limit = n
	}
	if v := c.Query("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "offset must be non-negative"})
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
}
