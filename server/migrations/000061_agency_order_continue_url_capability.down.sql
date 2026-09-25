DELETE FROM agency_order_provider_capabilities
WHERE capability_kind='UCP_CONTINUE_URL';

ALTER TABLE agency_order_provider_capabilities
    DROP CONSTRAINT agency_order_provider_capabilities_capability_kind_check;

ALTER TABLE agency_order_provider_capabilities
    ADD CONSTRAINT agency_order_provider_capabilities_capability_kind_check
    CHECK (capability_kind IN ('STOREFRONT_CART','UCP_CHECKOUT','BUYER_CONTEXT'));
