"""Synthetic schema exercises; these never prove live Best Buy integration."""

from copy import deepcopy
from decimal import Decimal
import unittest

from bestbuy_step1_probe import (
    ContractError, keyword_filter, normalize, observed_price, parse_collection,
    sku_ref, valid_product_link, variation_refs,
)


def headset():
    return {"sku": 10000001, "name": "Synthetic headset - Black",
            "salePrice": Decimal("79.99"), "onlineAvailability": True,
            "productVariations": [{"sku": 10000002}, {"sku": 10000003}],
            "url": "https://api.bestbuy.com/click/synthetic/10000001/pdp"}


class ContractProbeTest(unittest.TestCase):
    def test_exact_minor_units_and_source_reference(self):
        value = normalize(headset())
        self.assertEqual(value["price"]["amountMinor"], 7999)
        self.assertEqual(value["variantRef"], {"source": "BESTBUY", "sku": "10000001"})
        self.assertEqual(value["purchaseRoute"], "EXTERNAL")
        self.assertEqual(value["merchantStatus"], "UNVERIFIED")

    def test_missing_relations_are_unknown_not_single_variant_claim(self):
        row = headset()
        del row["productVariations"]
        self.assertEqual(variation_refs(row), ("UNKNOWN", []))

    def test_empty_relations_are_observed_empty(self):
        row = headset()
        row["productVariations"] = []
        self.assertEqual(variation_refs(row), ("EMPTY_OBSERVED", []))

    def test_relations_deduplicate_self_without_group_merging(self):
        row = headset()
        row["productVariations"] += [{"sku": 10000001}, {"sku": 10000002}]
        self.assertEqual(variation_refs(row), ("RELATED_REFS", ["10000002", "10000003"]))
        self.assertEqual(normalize(row)["sourceProductRef"], {"source": "BESTBUY", "sku": "10000001"})

    def test_related_sku_keeps_its_own_price_and_availability(self):
        root, other = headset(), headset()
        other.update(sku=10000002, salePrice=Decimal("89.95"), onlineAvailability=False)
        rows, _ = parse_collection({"products": [root, other]}, {"10000001", "10000002"})
        self.assertEqual([r["price"]["amountMinor"] for r in rows], [7999, 8995])
        self.assertEqual([r["availability"] for r in rows], ["AVAILABLE", "UNAVAILABLE"])

    def test_partial_lookup_does_not_fabricate_missing_variants(self):
        rows, partial = parse_collection({"products": [headset()], "partial": True},
                                         {"10000001", "10000002"})
        self.assertTrue(partial)
        self.assertEqual(len(rows), 1)

    def test_foreign_and_duplicate_sku_rejected(self):
        with self.assertRaisesRegex(ContractError, "UNREQUESTED_SKU"):
            parse_collection({"products": [headset()]}, {"10000002"})
        with self.assertRaisesRegex(ContractError, "DUPLICATE_SKU"):
            parse_collection({"products": [headset(), headset()]})

    def test_promotional_offers_are_not_seller_offers_or_variants(self):
        row = headset()
        baseline = normalize(row)
        row["offers"] = [{"id": "synthetic-promo", "type": "special_offer"}]
        self.assertEqual(normalize(row), baseline)

    def test_price_missing_or_restricted_stays_unknown(self):
        self.assertEqual(observed_price({})["reason"], "PRICE_MISSING")
        self.assertEqual(observed_price({"salePrice": 99, "priceRestriction": "MAP"})["reason"],
                         "PRICE_RESTRICTED")

    def test_bad_money_not_rounded_or_coerced(self):
        for amount in [True, 79.99, "NaN", "Infinity", "-1", "1.001", "not-a-price"]:
            with self.subTest(amount=amount), self.assertRaises(ContractError):
                observed_price({"salePrice": amount})

    def test_missing_boolean_not_coerced_to_false(self):
        row = headset()
        del row["onlineAvailability"]
        self.assertEqual(normalize(row)["availability"], "UNKNOWN")
        row["onlineAvailability"] = "false"
        with self.assertRaisesRegex(ContractError, "AVAILABILITY_TYPE_INVALID"):
            normalize(row)

    def test_urls_only_permit_product_navigation(self):
        self.assertTrue(valid_product_link(headset()["url"]))
        for url in ["https://api.bestbuy.com/click/synthetic/10000001/cart",
                    "https://bestbuy.com.evil.test/site/p", "http://www.bestbuy.com/site/p",
                    "https://user:password@www.bestbuy.com/site/p", "javascript:alert(1)",
                    "https://www.bestbuy.com:444/site/p", "https://www.bestbuy.com:bad/site/p"]:
            with self.subTest(url=url):
                self.assertFalse(valid_product_link(url))

    def test_bad_variation_shape_is_not_silent_empty(self):
        for relations in [{"sku": 10000002}, ["10000002"], [{"sku": False}]]:
            row = headset()
            row["productVariations"] = deepcopy(relations)
            with self.subTest(relations=relations), self.assertRaises(ContractError):
                variation_refs(row)

    def test_sku_preserves_full_identity_without_seven_digit_assumption(self):
        self.assertEqual(sku_ref(10000001), "10000001")
        for value in [True, 1.5, "001", "abc", "1/2", -1]:
            with self.subTest(value=value), self.assertRaises(ContractError):
                sku_ref(value)

    def test_query_does_not_accept_provider_filter_injection(self):
        self.assertEqual(keyword_filter("gaming headset"), "search=gaming&search=headset")
        for query in ["", "headset)|sku=*", "headset&active=*", "a b c d e f g"]:
            with self.subTest(query=query), self.assertRaises(ContractError):
                keyword_filter(query)


if __name__ == "__main__":
    unittest.main()
