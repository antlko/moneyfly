#!/usr/bin/env python3
"""Build the Excel parity fixture from the workbook.

Provenance for testdata/parity/workbook-2025-2026.json. The workbook itself is
not committed: it holds real personal finances, and every figure the tests need
is in the fixture. Re-run with the path to the sheet if it ever has to change:

    python3 testdata/parity/generate.py "~/Downloads/[2025-2026] Budget_ Capital Grow.xlsx"

Two things this script does NOT do: it never copies a derived value out of the
sheet as the expected answer, and it never rounds a result. Expected values are
recomputed here from the rounded cell inputs by the formula restated in
docs/07-metrics-and-budgets.md, and the sheet's own value is recorded alongside
whenever the two differ, with the deviation id that explains why.
"""
import json
import os
import sys
from decimal import Decimal, ROUND_HALF_UP

import openpyxl

DEFAULT = os.path.expanduser("~/Downloads/[2025-2026] Budget_ Capital Grow.xlsx")
COLS = list("EFGHIJKLMNOP")
PERIODS = ["2025-08", "2025-09", "2025-10", "2025-11", "2025-12",
           "2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06", "2026-07"]
CATEGORY_ROWS = range(9, 27)

# Canonical names, corrected spellings retained as aliases (appendix A.2).
CANONICAL = {"Applience": "Appliances", "Toilery": "Toiletry"}
ESSENTIAL = {"House", "Food", "Eating out", "Toilery", "Studying", "Hobby", "Clothes",
             "Transport", "Health", "Communications", "Sport", "Bills", "Services"}


def minor(value):
    """Round a sheet value to integer cents, half-up (adr/0004)."""
    return int(Decimal(repr(value)).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP) * 100)


def subcent(value):
    return Decimal(repr(value)) * 100 != Decimal(minor(value))


def money_entry(expected, sheet_value, deviations, exponent=2):
    """A money field: exact minor units, plus the sheet's figure when it differs."""
    out = {"expected_minor": expected}
    if sheet_value is not None:
        scale = Decimal(10) ** exponent
        sheet_minor = int((Decimal(repr(sheet_value)) * scale).quantize(
            Decimal("1"), rounding=ROUND_HALF_UP))
        if sheet_minor != expected:
            out["workbook"] = sheet_value
            out["workbook_minor"] = sheet_minor
            out["deviations"] = deviations
    return out


def ratio_entry(expected, sheet_value, deviations, tolerance=1e-6):
    """A ratio field: floats, with the sheet's figure when it differs beyond tolerance."""
    out = {"expected": expected}
    if sheet_value is not None:
        scale = max(abs(expected), abs(sheet_value), 1e-12)
        if abs(expected - sheet_value) / scale > tolerance:
            out["workbook"] = sheet_value
            out["deviations"] = deviations
    return out


# The capital half (appendix A.4-A.5). Native currency per row: the sheet stored
# children in their own currency and converted inside each parent's formula.
CAPITAL_ROWS = [
    # row, name, currency, liquid, counts, parent
    (36, "Gold", "EUR", False, True, None),
    (38, "Cash USD", "USD", True, True, "Cash"),
    (39, "Cash EUR", "EUR", True, True, "Cash"),
    (40, "Cash HUF", "HUF", True, True, "Cash"),
    (42, "Banks USD", "USD", True, True, "Banks"),
    (43, "Banks EUR", "EUR", True, True, "Banks"),
    (44, "Banks HUF", "HUF", True, True, "Banks"),
    (45, "Banks UAH", "UAH", True, True, "Banks"),
    (46, "Banks FOP", "UAH", True, True, "Banks"),
    # Row 47 "Deposits" carried a literal because rows 48-49 were labels with no
    # data. In the new chart it is a computed parent, so its one figure belongs to
    # the EUR child; the roll-up puts it back in the same place.
    (47, "Deposits EUR", "EUR", False, True, "Deposits"),
    (50, "Invests", "EUR", False, True, None),
    (66, "CSGO Skins", "EUR", False, True, None),
    (68, "Ton", "EUR", False, True, None),
    (69, "USDT (EUR)", "EUR", False, True, None),
]

# The sheet's settings block, rows 2-5: units of the foreign currency per euro is
# the inverse of what it stored.
SHEET_RATES = {"USD": 0.88, "HUF": 0.0028, "UAH": 0.02}

# HUF is 0-decimal; everything else here is 2.
EXPONENTS = {"EUR": 2, "USD": 2, "HUF": 0, "UAH": 2}


def capital_fixture(ws, recorded):
    """Rows 36-72: snapshots, the roll-ups and the figures they feed."""
    accounts = []
    for row, name, code, liquid, counts, parent in CAPITAL_ROWS:
        native, base = {}, {}
        for period, col in zip(PERIODS, COLS):
            value = ws[f"{col}{row}"].value
            # An unfilled month reads 0 across the capital block, exactly the
            # sentinel fault D1 describes. It is an absence here.
            if value is None or not recorded[period]:
                continue
            scale = Decimal(10) ** EXPONENTS[code]
            native_minor = int((Decimal(repr(value)) * scale).quantize(Decimal("1"), rounding=ROUND_HALF_UP))
            base[period] = int((Decimal(repr(value)) * Decimal(repr(SHEET_RATES.get(code, 1.0)))
                                * 100).quantize(Decimal("1"), rounding=ROUND_HALF_UP))
            native[period] = native_minor
        accounts.append({
            "row": row, "name": name, "currency": code, "exponent": EXPONENTS[code],
            "is_liquid": liquid, "counts_toward_net_worth": counts, "parent": parent,
            "native": native, "base": base,
        })

    ready, general, general_uah, general_huf, runway, allocation = {}, {}, {}, {}, {}, {}
    for period, col in zip(PERIODS, COLS):
        if not recorded[period]:
            continue
        liquid_sum = sum(a["base"].get(period, 0) for a in accounts if a["is_liquid"])
        total = sum(a["base"].get(period, 0) for a in accounts if a["counts_toward_net_worth"])
        ready[period] = money_entry(liquid_sum, ws[f"{col}54"].value, ["D11"])
        general[period] = money_entry(total, ws[f"{col}55"].value, ["D11"])
        general_uah[period] = money_entry(
            int((Decimal(total) / Decimal(repr(SHEET_RATES["UAH"]))).quantize(Decimal("1"), rounding=ROUND_HALF_UP)),
            ws[f"{col}71"].value, ["D11"])
        # HUF is 0-decimal, so the figure is in whole forint.
        general_huf[period] = money_entry(
            int((Decimal(total) / Decimal(repr(SHEET_RATES["HUF"])) / 100).quantize(Decimal("1"), rounding=ROUND_HALF_UP)),
            ws[f"{col}72"].value, ["D11"], exponent=0)
        runway[period] = {"workbook": ws[f"{col}57"].value}
        allocation[period] = {
            a["name"]: a["base"][period] / total
            for a in accounts if a["counts_toward_net_worth"] and period in a["base"] and total
        }

    return {
        "rates": SHEET_RATES,
        "accounts": accounts,
        "ready_for_usage": ready,
        "general": general,
        "general_in": {"UAH": general_uah, "HUF": general_huf},
        "allocation": allocation,
        "runway_legacy_blend": runway,
        "workbook_allocation_sum": sum(
            ws[f"C{row}"].value or 0 for row in (36, 37, 38, 39, 40, 41, 46, 47, 50)
        ),
        "workbook_cash_huf_share": ws["C40"].value,
    }


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else DEFAULT
    ws = openpyxl.load_workbook(path, data_only=True)["Budget"]

    recorded = {}
    for period, col in zip(PERIODS, COLS):
        # The sheet marks an unfilled month with -1 in every expense row. Here
        # that is simply an unrecorded period.
        recorded[period] = ws[f"{col}9"].value != -1

    categories = []
    rounding_hit = 0
    for row in CATEGORY_ROWS:
        sheet_name = ws[f"A{row}"].value
        spend, spend_sheet = {}, {}
        for period, col in zip(PERIODS, COLS):
            value = ws[f"{col}{row}"].value
            if value is None or not recorded[period]:
                continue
            if subcent(value):
                rounding_hit += 1
            spend[period] = minor(value)
            spend_sheet[period] = value

        total_expected = sum(spend.values())
        count = sum(1 for p in PERIODS if recorded[p])
        average_expected = total_expected / 100 / count if count else None

        total_sheet = ws[f"T{row}"].value
        deviations = ["D1"]  # the sheet's SUM swallows the -1 sentinel
        if any(subcent(v) for v in spend_sheet.values()):
            deviations.append("D11")

        categories.append({
            "row": row,
            "sheet_name": sheet_name,
            "name": CANONICAL.get(sheet_name, sheet_name),
            "essential": sheet_name in ESSENTIAL,
            "planned_minor": minor(ws[f"B{row}"].value),
            "spend": spend,
            "spend_sheet": spend_sheet,
            "average": ratio_entry(average_expected, ws[f"C{row}"].value, ["D11"]),
            "total": money_entry(total_expected, total_sheet, deviations),
        })

    spend_total, income, diff, saved = {}, {}, {}, {}
    for period, col in zip(PERIODS, COLS):
        if not recorded[period]:
            continue
        total = sum(c["spend"].get(period, 0) for c in categories)
        spend_total[period] = money_entry(total, ws[f"{col}27"].value, ["D2", "D11"])

        income_value = ws[f"{col}31"].value
        income_minor = minor(income_value) if income_value is not None else 0
        income[period] = {"expected_minor": income_minor}
        diff[period] = money_entry(income_minor - total, ws[f"{col}32"].value, ["D11"])
        if income_minor:
            saved[period] = ratio_entry(1 - total / income_minor, ws[f"{col}33"].value, ["D11"])

    recorded_count = sum(1 for p in PERIODS if recorded[p])
    spend_total_sum = sum(v["expected_minor"] for v in spend_total.values())
    planned_total = sum(c["planned_minor"] for c in categories)
    possible_minimum = sum(c["planned_minor"] for c in categories if c["essential"])
    income_planned = minor(ws["B31"].value)

    fixture = {
        "source": "[2025-2026] Budget_ Capital Grow.xlsx, sheet Budget",
        "generated_by": "testdata/parity/generate.py",
        "base_currency": "EUR",
        "exponent": 2,
        "periods": PERIODS,
        "recorded": recorded,
        "rounding": {
            "rule": "half-up to integer cents",
            "sub_cent_cells": rounding_hit,
            "deviation": "D11",
        },
        "categories": categories,
        "rollups": {
            "planned_total": money_entry(planned_total, ws["B27"].value, ["D11"]),
            "possible_minimum": money_entry(possible_minimum, ws["B28"].value, ["D3"]),
            "possible_minimum_average": ratio_entry(
                possible_minimum / 100, ws["C28"].value, ["D3"]),
            "spend_total": spend_total,
            "spend_total_average": ratio_entry(
                spend_total_sum / 100 / recorded_count, ws["C27"].value, ["D11"]),
            "income": income,
            "income_planned": money_entry(income_planned, ws["B31"].value, ["D11"]),
            "diff": diff,
            "diff_planned": money_entry(income_planned - planned_total, ws["B32"].value, ["D11"]),
            "saved_percent": saved,
            "saved_percent_planned": ratio_entry(
                1 - planned_total / income_planned, ws["B33"].value, ["D11"]),
        },
        "capital": capital_fixture(ws, recorded),
    }

    out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "workbook-2025-2026.json")
    with open(out, "w", encoding="utf-8") as f:
        json.dump(fixture, f, indent=2, ensure_ascii=False, sort_keys=False)
        f.write("\n")
    print(f"wrote {out}: {len(categories)} categories, {recorded_count} recorded periods, "
          f"{rounding_hit} sub-cent cells")


if __name__ == "__main__":
    main()
