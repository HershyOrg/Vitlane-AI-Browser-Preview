ALTER TABLE research_submissions
    DROP CONSTRAINT research_submissions_current_schema_check,
    ADD CONSTRAINT research_submissions_current_schema_check
        CHECK (
            validation_status = 'REJECTED'
            OR schema_version IN (
                'vitlane.research-submission.v3',
                'vitlane.research-submission.v4'
            )
        );
