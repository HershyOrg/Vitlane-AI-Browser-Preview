-- 단계 공개 (ADR-0083 PR 5): 수집 직후 미평가로 먼저 공개된 후보는 어느 Round의
-- 평가를 기다리는지 기억한다. 평가가 채워지거나 Round가 종결되면 비운다. 재시도
-- attempt는 이 표시로 같은 Round의 후보를 다시 골라 평가한다.
ALTER TABLE phase8_research_candidates
    ADD COLUMN evaluation_round_id UUID REFERENCES research_rounds(id) ON DELETE SET NULL;
CREATE INDEX phase8_research_candidates_evaluation_round_idx
    ON phase8_research_candidates(evaluation_round_id) WHERE evaluation_round_id IS NOT NULL;
