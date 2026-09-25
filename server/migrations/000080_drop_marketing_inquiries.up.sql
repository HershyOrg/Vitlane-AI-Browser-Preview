-- 공개 문의 제품은 UI·API·운영 surface와 함께 종료한다. 보관 중인 문의 본문과
-- 이메일도 더 이상 처리 목적이 없으므로 table을 삭제해 함께 폐기한다.
DROP TABLE IF EXISTS marketing_inquiries;
