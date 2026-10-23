"""What the shared fixtures cannot show: Python values in, and the decoder's refusals."""

from __future__ import annotations

import datetime
import json
from typing import Any, ClassVar

import pytest

from flagnetic import Context
from flagnetic._eval import RulesetError, decode, evaluate
from flagnetic._eval.bucket import bucket


def ruleset(attribute_type: str, op: str, value: Any, **flag: Any) -> Any:
    """A ruleset whose one flag serves "yes" when attribute x passes the condition."""
    document = {
        "schema_version": 1,
        "version": 1,
        "scope": "server",
        "unit_types": ["user"],
        "attributes": [{"key": "x", "type": attribute_type}],
        "flags": [
            {
                "key": "f",
                "type": "string",
                "on": True,
                "variations": [{"key": "yes", "value": "yes"}, {"key": "no", "value": "no"}],
                "off_variation": "no",
                "fallthrough": "no",
                "rules": [
                    {
                        "id": "r1",
                        "conditions": [{"kind": "attribute", "attribute": "x", "op": op, "value": value}],
                        "serve": {"kind": "variation", "variation": "yes"},
                    }
                ],
                **flag,
            }
        ],
    }
    return decode(json.dumps(document))


def serves_yes(rs: Any, **attributes: Any) -> bool:
    return evaluate(rs, Context(attributes=attributes), "f").variation == "yes"


class TestNumbers:
    def test_a_boolean_is_never_a_number(self) -> None:
        rs = ruleset("number", "eq", 1)
        assert serves_yes(rs, x=1)
        assert not serves_yes(rs, x=True)

    def test_integers_beyond_2_to_the_53_compare_as_doubles(self) -> None:
        rs = ruleset("number", "eq", 2**53 + 1)
        assert serves_yes(rs, x=2**53)
        assert serves_yes(rs, x=float(2**53))

    def test_an_integer_too_big_for_a_double_is_not_a_number(self) -> None:
        rs = ruleset("number", "gt", 0)
        assert not serves_yes(rs, x=10**400)

    @pytest.mark.parametrize("value", [float("nan"), float("inf"), "5", None])
    def test_other_values_are_wrong_typed(self, value: Any) -> None:
        rs = ruleset("number", "neq", 5)
        assert not serves_yes(rs, x=value)


class TestDates:
    def test_an_aware_datetime_is_an_instant(self) -> None:
        rs = ruleset("date", "after", "2025-01-01T00:00:00Z")
        eastern = datetime.timezone(datetime.timedelta(hours=5))
        assert serves_yes(rs, x=datetime.datetime(2025, 1, 1, 5, 0, 1, tzinfo=eastern))
        assert not serves_yes(rs, x=datetime.datetime(2025, 1, 1, 4, 59, 59, tzinfo=eastern))

    def test_a_naive_datetime_is_wrong_typed(self) -> None:
        rs = ruleset("date", "after", "2025-01-01T00:00:00Z")
        assert not serves_yes(rs, x=datetime.datetime(2030, 1, 1))

    def test_epoch_milliseconds_are_integral(self) -> None:
        rs = ruleset("date", "after", "1970-01-01T00:00:01Z")
        assert serves_yes(rs, x=1001)
        assert not serves_yes(rs, x=1000)
        assert not serves_yes(rs, x=1500.5)

    def test_digits_of_other_scripts_are_not_a_date(self) -> None:
        rs = ruleset("date", "after", "2025-01-01T00:00:00Z")
        assert not serves_yes(rs, x="٢٠٣٠-٠١-٠١T00:00:00Z")


class TestSemver:
    def test_components_of_any_size_compare_as_numbers(self) -> None:
        big = "1" + "0" * 5000
        rs = ruleset("semver", "lt", f"{big}.0.0")
        assert serves_yes(rs, x=f"9{'0' * 4999}.0.0")
        assert not serves_yes(rs, x=f"{big}.0.1")


class TestUnitsAndLists:
    def test_a_unit_type_is_a_string_attribute_holding_the_unit_id(self) -> None:
        rs = decode(
            {
                "schema_version": 1,
                "version": 1,
                "scope": "server",
                "unit_types": ["user"],
                "flags": [
                    {
                        "key": "f",
                        "type": "string",
                        "on": True,
                        "variations": [{"key": "yes", "value": "yes"}],
                        "off_variation": "yes",
                        "fallthrough": "yes",
                        "rules": [
                            {
                                "id": "r1",
                                "conditions": [
                                    {"kind": "attribute", "attribute": "user", "op": "eq", "value": "u1"}
                                ],
                                "serve": {"kind": "variation", "variation": "yes"},
                            }
                        ],
                    }
                ],
            }
        )
        assert evaluate(rs, Context(units={"user": "u1"}), "f").reason == "rule"
        assert evaluate(rs, Context(units={"user": ""}), "f").reason == "fallthrough"

    def test_a_string_is_not_a_list(self) -> None:
        rs = ruleset("list", "contains", "a")
        assert serves_yes(rs, x=["a", "b"])
        assert serves_yes(rs, x=("a",))
        assert not serves_yes(rs, x="a")
        assert not serves_yes(rs, x=["a", 1])


class TestBucket:
    def test_a_lone_surrogate_hashes_as_the_replacement_character(self) -> None:
        assert bucket("salt", "a\ud800b") == bucket("salt", "a�b")

    def test_unit_ids_hash_as_utf8(self) -> None:
        assert bucket("s", "é") == bucket("s", "é")


class TestDecode:
    DOCUMENT: ClassVar[dict[str, Any]] = {"schema_version": 1, "version": 3, "scope": "server", "flags": []}

    def test_decodes_text_bytes_and_objects(self) -> None:
        text = json.dumps(self.DOCUMENT)
        assert decode(text).version == 3
        assert decode(text.encode()).version == 3
        assert decode(self.DOCUMENT).version == 3

    @pytest.mark.parametrize(
        "document",
        [
            "{",
            "[]",
            "null",
            '{"schema_version": 2, "scope": "server"}',
            '{"schema_version": true, "scope": "server"}',
            '{"schema_version": 1, "scope": "client"}',
            '{"schema_version": 1, "scope": "server", "flags": {}}',
            '{"schema_version": 1, "scope": "server", "flags": [{"key": 1}]}',
            '{"schema_version": 1, "scope": "server", "flags": [{"key": "f", "on": "yes"}]}',
            '{"schema_version": 1, "scope": "server", "unit_types": "user"}',
            '{"schema_version": 1.0, "scope": "server"}',
            '{"schema_version": 1, "version": 9223372036854775808, "scope": "server"}',
            '{"schema_version": 1, "version": -9223372036854775809, "scope": "server"}',
            '{"schema_version": 1, "scope": "server", "flags": [{"variations": [{"value": NaN}]}]}',
            '{"schema_version": 1, "scope": "server", "flags": [{"variations": [{"value": -Infinity}]}]}',
            '\ufeff{"schema_version": 1, "scope": "server"}',
        ],
    )
    def test_refuses_what_it_cannot_read(self, document: str) -> None:
        with pytest.raises(RulesetError):
            decode(document)

    def test_reads_bytes_as_utf8_only(self) -> None:
        text = '{"schema_version": 1, "scope": "server"}'
        with pytest.raises(RulesetError):
            decode(text.encode("utf-16"))
        with pytest.raises(RulesetError):
            decode(text.encode("utf-8-sig"))

    def test_invalid_utf8_in_a_string_reads_as_the_replacement_character(self) -> None:
        document = b'{"schema_version": 1, "scope": "server", "flags": [{"key": "f\xff"}]}'
        assert decode(document).keys == ("f�",)

    def test_the_widest_integer_is_read(self) -> None:
        assert decode({**self.DOCUMENT, "version": (1 << 63) - 1}).version == (1 << 63) - 1

    def test_null_members_read_as_their_zero_value(self) -> None:
        flag = {
            "key": "f",
            "type": "string",
            "on": True,
            "variations": [{"key": "a", "value": "A"}],
            "fallthrough": "a",
        }
        rs = decode(
            {
                **self.DOCUMENT,
                "environment": None,
                "unit_types": [None, "user"],
                "attributes": [None],
                "flags": [None, flag, {**flag, "key": "g", "rules": [{"id": "r", "serve": None}]}],
            }
        )
        assert rs.keys == ("", "f", "g")
        assert evaluate(rs, Context(), "f").variation == "a"
        assert evaluate(rs, Context(), "g").error_code == "inconsistent_ruleset"
        broken = {**flag, "rules": [{"id": "r", "conditions": [None], "serve": {"kind": "variation"}}]}
        assert evaluate(decode({**self.DOCUMENT, "flags": [broken]}), Context(), "f").error_code == (
            "inconsistent_ruleset"
        )

    def test_an_unknown_flag_is_an_error_without_type(self) -> None:
        result = evaluate(decode(self.DOCUMENT), Context(), "missing")
        assert result.as_dict() == {
            "reason": "error",
            "error_code": "flag_not_found",
            "override_influenced": False,
        }

    def test_environment_protection_is_read(self) -> None:
        rs = decode({**self.DOCUMENT, "environment": {"key": "prod", "protected": True}})
        assert (rs.environment_key, rs.protected) == ("prod", True)
        assert decode(self.DOCUMENT).protected is False

    def test_an_invalid_pattern_breaks_only_its_flag(self) -> None:
        rs = ruleset("string", "matches", "(?=a)")
        assert evaluate(rs, Context(attributes={"x": "a"}), "f").error_code == "inconsistent_ruleset"
        assert rs.unsupported_rules() == {}
