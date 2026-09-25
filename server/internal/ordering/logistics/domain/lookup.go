package domain

import "errors"

// ErrResolutionNotFound는 참조 id의 배송 판정이 없음이다(SUPPORT executor 조회).
var ErrResolutionNotFound = errors.New("LOGISTICS_RESOLUTION_NOT_FOUND")
