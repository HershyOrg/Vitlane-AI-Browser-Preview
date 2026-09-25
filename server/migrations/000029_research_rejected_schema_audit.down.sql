DELETE FROM research_submissions
WHERE validation_status = 'REJECTED'
  AND schema_version NOT IN (
      'vitlane.research-submission.v3',
      'vitlane.research-submission.v4'
  );

ALTER TABLE research_submissions
    DROP CONSTRAINT research_submissions_current_schema_check,
    ADD CONSTRAINT research_submissions_current_schema_check
        CHECK (
            schema_version IN (
                'vitlane.research-submission.v3',
                'vitlane.research-submission.v4'
            )
        );
