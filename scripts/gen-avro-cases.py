#!/usr/bin/env python3
"""Generate testdata/avro/apache-avro-cases.json.

The reader/writer pairs and expected results are transcribed from Apache
Avro's Java test suite (Apache License 2.0):
  lang/java/avro/src/test/java/org/apache/avro/TestSchemas.java
  TestSchemaCompatibility.java (COMPATIBLE_READER_WRITER_TEST_CASES)
  TestSchemaCompatibility{TypeMismatch,MissingEnumSymbols,
    ReaderFieldMissingDefaultValue,FixedSizeMismatch,NameMismatch,
    MissingUnionBranch}.java
Run: python3 scripts/gen-avro-cases.py > testdata/avro/apache-avro-cases.json
"""
import json

NULL, BOOLEAN, INT, LONG, FLOAT, DOUBLE, STRING, BYTES = (
    "null", "boolean", "int", "long", "float", "double", "string", "bytes")

def arr(t): return {"type": "array", "items": t}
def mp(t): return {"type": "map", "values": t}
def enum(name, syms, ns=None, default=None):
    e = {"type": "enum", "name": name, "symbols": syms}
    if ns: e["namespace"] = ns
    if default is not None: e["default"] = default
    return e
def rec(name, fields, ns=None):
    r = {"type": "record", "name": name, "fields": fields}
    if ns: r["namespace"] = ns
    return r
def f(name, t, default=None, has_default=False):
    d = {"name": name, "type": t}
    if has_default: d["default"] = default
    return d
def fixed(name, size): return {"type": "fixed", "name": name, "size": size}

INT_ARRAY, LONG_ARRAY = arr(INT), arr(LONG)
INT_MAP, LONG_MAP = mp(INT), mp(LONG)
ENUM1_AB = enum("Enum1", ["A", "B"])
ENUM1_AB_DEFAULT = enum("Enum1", ["A", "B"], default="A")
ENUM1_AB_NS1 = enum("Enum1", ["A", "B"], ns="namespace1")
ENUM1_AB_NS2 = enum("Enum1", ["A", "B"], ns="namespace2")
ENUM1_ABC = enum("Enum1", ["A", "B", "C"])
ENUM1_BC = enum("Enum1", ["B", "C"])
ENUM2_AB = enum("Enum2", ["A", "B"])
EMPTY_UNION = []
INT_UNION, LONG_UNION, FLOAT_UNION, DOUBLE_UNION = [INT], [LONG], [FLOAT], [DOUBLE]
STRING_UNION, BYTES_UNION = [STRING], [BYTES]
INT_STRING_UNION, STRING_INT_UNION = [INT, STRING], [STRING, INT]
INT_FLOAT_UNION, INT_LONG_UNION = [INT, FLOAT], [INT, LONG]
INT_LONG_FLOAT_DOUBLE_UNION = [INT, LONG, FLOAT, DOUBLE]
FIXED_4, FIXED_8 = fixed("Fixed", 4), fixed("Fixed", 8)
FIXED_4_ANOTHER = fixed("AnotherName", 4)

EMPTY_RECORD1 = rec("Record1", [])
EMPTY_RECORD2 = rec("Record2", [])
A_INT = rec("Record1", [f("a", INT)])
A_LONG = rec("Record1", [f("a", LONG)])
A_INT_B_INT = rec("Record1", [f("a", INT), f("b", INT)])
A_DINT = rec("Record1", [f("a", INT, 0, True)])
A_INT_B_DINT = rec("Record1", [f("a", INT), f("b", INT, 0, True)])
A_DINT_B_DINT = rec("Record1", [f("a", INT, 0, True), f("b", INT, 0, True)])
A_DINT_B_DFIXED_4 = rec("Record1", [f("a", INT, 0, True), f("b", FIXED_4)])
A_DINT_B_DFIXED_8 = rec("Record1", [f("a", INT, 0, True), f("b", FIXED_8)])
A_DINT_B_DINT_STRING_UNION = rec("Record1", [f("a", INT, 0, True), f("b", INT_STRING_UNION, 0, True)])
A_DINT_B_DINT_UNION = rec("Record1", [f("a", INT, 0, True), f("b", INT_UNION, 0, True)])
A_DINT_B_DENUM_1 = rec("Record1", [f("a", INT, 0, True), f("b", ENUM1_AB)])
A_DINT_B_DENUM_2 = rec("Record1", [f("a", INT, 0, True), f("b", ENUM2_AB)])
INT_LIST = rec("List", [f("head", INT), f("tail", "List")])
LONG_LIST = rec("List", [f("head", LONG), f("tail", "List")])
NS_RECORD1 = rec("Record1", [f("f1", [NULL, arr(rec("InnerRecord1", [f("a", INT)], ns="ns1"))])])
NS_RECORD2 = rec("Record1", [f("f1", [NULL, arr(rec("InnerRecord1", [f("a", INT)], ns="ns2"))])])
WITHOUT_NS = rec("Record", [f("f1", INT)])
WITH_NS = rec("ns.Record", [f("f1", INT)])
ENUM_ABC_ENUM_DEFAULT_A_RECORD = rec("Record", [f("Field", enum("Schema", ["A", "B", "C"], default="A"))])
ENUM_AB_ENUM_DEFAULT_A_RECORD = rec("Record", [f("Field", enum("Schema", ["A", "B"], default="A"))])
ENUM_ABC_FIELD_DEFAULT_B_ENUM_DEFAULT_A_RECORD = rec("Record", [f("Field", enum("Schema", ["A", "B", "C"], default="A"), "B", True)])
ENUM_AB_FIELD_DEFAULT_A_ENUM_DEFAULT_B_RECORD = rec("Record", [f("Field", enum("Schema", ["A", "B"], default="B"), "A", True)])
RECORD1_WITH_ENUM_AB = rec("Record1", [f("field1", ENUM1_AB)])
RECORD1_WITH_ENUM_ABC = rec("Record1", [f("field1", ENUM1_ABC)])
UNION_INT_RECORD1 = [INT, rec("Record1", [f("field1", INT)])]
UNION_INT_RECORD2 = [INT, rec("Record2", [f("field1", INT)])]
UNION_INT_ENUM1_AB = [INT, ENUM1_AB]
UNION_INT_FIXED_4 = [INT, FIXED_4]
UNION_INT_BOOLEAN = [INT, BOOLEAN]
UNION_INT_ARRAY_INT = [INT, INT_ARRAY]
UNION_INT_MAP_INT = [INT, INT_MAP]
UNION_INT_NULL = [INT, NULL]

compatible = [
    (BOOLEAN, BOOLEAN), (INT, INT), (LONG, INT), (LONG, LONG),
    (FLOAT, INT), (FLOAT, LONG), (DOUBLE, LONG), (DOUBLE, INT), (DOUBLE, FLOAT),
    (STRING, STRING), (BYTES, BYTES),
    (INT_ARRAY, INT_ARRAY), (LONG_ARRAY, INT_ARRAY), (INT_MAP, INT_MAP), (LONG_MAP, INT_MAP),
    (ENUM1_AB, ENUM1_AB), (ENUM1_ABC, ENUM1_AB), (ENUM1_AB_DEFAULT, ENUM1_ABC),
    (ENUM1_AB, ENUM1_AB_NS1), (ENUM1_AB_NS1, ENUM1_AB), (ENUM1_AB_NS1, ENUM1_AB_NS2),
    (STRING, BYTES), (BYTES, STRING),
    (EMPTY_UNION, EMPTY_UNION), (FLOAT_UNION, EMPTY_UNION), (FLOAT_UNION, INT_UNION),
    (FLOAT_UNION, LONG_UNION), (FLOAT_UNION, INT_LONG_UNION), (INT_UNION, INT_UNION),
    (INT_STRING_UNION, STRING_INT_UNION), (INT_UNION, EMPTY_UNION), (LONG_UNION, EMPTY_UNION),
    (LONG_UNION, INT_UNION), (DOUBLE_UNION, INT_UNION), (DOUBLE_UNION, LONG_UNION),
    (DOUBLE_UNION, FLOAT_UNION), (STRING_UNION, EMPTY_UNION), (STRING_UNION, BYTES_UNION),
    (BYTES_UNION, EMPTY_UNION), (BYTES_UNION, STRING_UNION), (DOUBLE_UNION, INT_FLOAT_UNION),
    (FLOAT, INT_FLOAT_UNION), (LONG, INT_LONG_UNION), (DOUBLE, INT_FLOAT_UNION),
    (DOUBLE, INT_LONG_FLOAT_DOUBLE_UNION), (FLOAT, FLOAT_UNION), (INT_UNION, INT), (INT, INT_UNION),
    (FIXED_4, FIXED_4),
    (EMPTY_RECORD1, EMPTY_RECORD1), (EMPTY_RECORD1, A_INT), (A_INT, A_INT), (A_DINT, A_INT),
    (A_DINT, A_DINT), (A_INT, A_DINT), (A_LONG, A_INT), (A_INT, A_INT_B_INT), (A_DINT, A_INT_B_INT),
    (A_INT_B_DINT, A_INT), (A_DINT_B_DINT, EMPTY_RECORD1), (A_DINT_B_DINT, A_INT),
    (A_INT_B_INT, A_DINT_B_DINT),
    (INT_LIST, INT_LIST), (LONG_LIST, LONG_LIST), (LONG_LIST, INT_LIST),
    (NULL, NULL),
    (ENUM_AB_ENUM_DEFAULT_A_RECORD, ENUM_ABC_ENUM_DEFAULT_A_RECORD),
    (ENUM_AB_FIELD_DEFAULT_A_ENUM_DEFAULT_B_RECORD, ENUM_ABC_FIELD_DEFAULT_B_ENUM_DEFAULT_A_RECORD),
    (NS_RECORD1, NS_RECORD2), (WITHOUT_NS, WITH_NS),
]

incompatible = []
def inc(kind, pairs):
    for r, w in pairs:
        incompatible.append((r, w, kind))

inc("TYPE_MISMATCH", [
    (NULL, INT), (NULL, LONG), (BOOLEAN, INT), (INT, NULL), (INT, BOOLEAN), (INT, LONG),
    (INT, FLOAT), (INT, DOUBLE), (LONG, FLOAT), (LONG, DOUBLE), (FLOAT, DOUBLE),
    (DOUBLE, STRING), (FIXED_4, STRING), (STRING, BOOLEAN), (STRING, INT), (BYTES, NULL),
    (BYTES, INT), (A_INT, INT), (INT_ARRAY, LONG_ARRAY), (INT_MAP, INT_ARRAY),
    (INT_ARRAY, INT_MAP), (INT_MAP, LONG_MAP), (INT, ENUM2_AB), (ENUM2_AB, INT),
    (FLOAT, INT_LONG_FLOAT_DOUBLE_UNION), (LONG, INT_FLOAT_UNION), (INT, INT_FLOAT_UNION),
    (INT_LIST, LONG_LIST),
])
inc("MISSING_ENUM_SYMBOLS", [(ENUM1_AB, ENUM1_ABC), (ENUM1_BC, ENUM1_ABC), (RECORD1_WITH_ENUM_AB, RECORD1_WITH_ENUM_ABC)])
inc("READER_FIELD_MISSING_DEFAULT_VALUE", [(A_INT, EMPTY_RECORD1), (A_INT_B_DINT, EMPTY_RECORD1)])
inc("FIXED_SIZE_MISMATCH", [(FIXED_4, FIXED_8), (FIXED_8, FIXED_4), (A_DINT_B_DFIXED_8, A_DINT_B_DFIXED_4), (A_DINT_B_DFIXED_4, A_DINT_B_DFIXED_8)])
inc("NAME_MISMATCH", [(ENUM1_AB, ENUM2_AB), (EMPTY_RECORD2, EMPTY_RECORD1), (FIXED_4, FIXED_4_ANOTHER), (A_DINT_B_DENUM_1, A_DINT_B_DENUM_2)])
inc("MISSING_UNION_BRANCH", [
    (INT_UNION, INT_STRING_UNION), (STRING_UNION, INT_STRING_UNION), (INT_UNION, UNION_INT_RECORD1),
    (INT_UNION, UNION_INT_RECORD2), (UNION_INT_RECORD1, UNION_INT_RECORD2), (INT_UNION, UNION_INT_ENUM1_AB),
    (INT_UNION, UNION_INT_FIXED_4), (INT_UNION, UNION_INT_BOOLEAN), (INT_UNION, LONG_UNION),
    (INT_UNION, FLOAT_UNION), (INT_UNION, DOUBLE_UNION), (INT_UNION, BYTES_UNION),
    (INT_UNION, UNION_INT_ARRAY_INT), (INT_UNION, UNION_INT_MAP_INT), (INT_UNION, UNION_INT_NULL),
    (INT_UNION, INT_LONG_FLOAT_DOUBLE_UNION), (A_DINT_B_DINT_UNION, A_DINT_B_DINT_STRING_UNION),
])

out = {
    "source": "Transcribed from Apache Avro's Java test suite (Apache License 2.0): TestSchemas.java, TestSchemaCompatibility*.java",
    "cases": [{"reader": r, "writer": w, "compatible": True} for r, w in compatible]
    + [{"reader": r, "writer": w, "compatible": False, "type": k} for r, w, k in incompatible],
}
print(json.dumps(out, indent=1))
