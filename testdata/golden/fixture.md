<!-- schema-evolution-gate -->
### ❌ Schema evolution gate failed: 3 breaking changes, 2 of 4 topics failing

Compared with `main (<base-sha>)`.

| Topic | Subject | Format | Mode | Result |
|---|---|---|---|---|
| `orders` | `orders-value` | Avro | `FULL_TRANSITIVE` | ❌ 3 breaking, replay 4/4 failed |
| `payments` | `payments-value` | JSON Schema | `BACKWARD` | ❌ compatible by rules, replay 1/3 failed |
| `refunds` | `refunds-value` | Avro | `BACKWARD` | ➖ unchanged |
| `customers` | `customers-value` | JSON Schema | `BACKWARD` | ✅ compatible (1 non-breaking change) |

#### `orders-value` (`schemas/orders/order-placed.avsc`)

| Rule | Field | Direction | Against | Why |
|---|---|---|---|---|
| `READER_FIELD_MISSING_DEFAULT_VALUE` | `OrderPlaced.currency` | backward | `base, order-placed.v1.avsc` | new field `currency` has no default, so the new schema cannot read older events that lack it |
| `MISSING_ENUM_SYMBOLS` | `OrderPlaced.status` | backward | `base, order-placed.v1.avsc` | enum symbol removed from `status`; older events that use it cannot be read |
| `TYPE_MISMATCH` | `OrderPlaced.lines[].quantity` | forward | `base, order-placed.v1.avsc` | type of `lines[].quantity` changed: data written as long cannot be read as int |

**Replay:** 4 of 4 recorded payloads fail against the new schema.

| Sample | Path | Error |
|---|---|---|
| `order-placed.jsonl:1` | `/currency` | field "currency" is not in the payload and has no default |
| `order-placed.jsonl:2` | `/currency` | field "currency" is not in the payload and has no default |
| `order-placed.jsonl:3` | `/currency` | field "currency" is not in the payload and has no default |
| `order-placed.jsonl:3` | `/status` | enum symbol "REFUNDED" no longer exists in shop.orders.OrderStatus |
| `order-placed.jsonl:4` | `/currency` | field "currency" is not in the payload and has no default |

#### `payments-value` (`schemas/payments/payment-captured.schema.json`)

**Replay:** 1 of 3 recorded payloads fail against the new schema.

| Sample | Path | Error |
|---|---|---|
| `payment-captured.jsonl:2` | `/risk_score` | got string, want integer |

<details><summary>How to read this</summary>

- **backward**: consumers on the new schema reading events written with an older version. Breaks here mean old events on the topic (retention, replays, rewinds) become unreadable.
- **forward**: consumers still on an older schema reading events from the new one. Breaks here mean producers cannot ship before every consumer upgrades.
- Rules follow Confluent Schema Registry compatibility semantics (Avro resolution rules; JSON Schema diff types and policy). Replay validates recorded payloads from the repo against the proposed schema.
</details>

<sub>schema-evolution-gate test</sub>
