# Lifecycle history store: checkpoint 1

This document describes the implemented DynamoDB adapter in `internal/audit`.
The [accepted design](lifecycle-history.md) remains the requirements source.
The adapter is not a supported runtime mode yet.
Checkpoint 2 must add durable delivery and operation gates before runtime use.

## Interface and authority

`NewDynamoDB` requires an SDK client, table, organization ID, and environment ID.
The adapter implements the existing `Sink` interface for event writes and searches.
It adds `PutJourney`, `GetJourney`, and `ResolveJourney` for durable metadata.
All reads use DynamoDB's strongly consistent read option.
There is no local fallback.

`PutJourney` accepts revision zero for a new journey.
For an update, the caller supplies the last read revision.
A conditional transaction advances the revision and claims all exact correlations together.
A stale revision or a correlation claimed by another journey returns `ErrConflict`.
After an uncertain write response, the caller reads the journey or its exact correlation again.

A journey stores its origin, start time, searchable name, identity correlations, ownership observations, and optional terminal outcome.
Its origin is `request`, `direct`, or `adoption`.
An ownership observation records when a source established ownership.
It does not prove when an unobserved transfer occurred.
The source is a request queue record or provider account tags, with an exact reference.
An empty observed owner records missing ownership evidence.
Event owner and actor labels cannot create ownership observations.

Identity correlations connect creation operation IDs, provider request IDs, and provider account IDs to a journey.
A request submission uses its journey ID, not its reusable queue tag or name.
A journey can connect to one provider account.
Correlations and ownership observations are append-only.
Origin, start time, and accepted terminal outcomes are immutable.
Names can change without changing identity.

Terminal metadata accepts confirmed account closure or denial, withdrawal, or expiry of an unfulfilled request.
It rejects account expiry, closure intent, and provisioning failure as terminal outcomes.
The producer must supply reliable evidence for these assertions.
The adapter checks structure and exact references, not the truth of an AWS observation.
Checkpoint 2 must establish those recording semantics.

Core continues to derive lifecycle decisions from provider facts and tags.
The adapter does not supply budgets, expiry, placement instructions, or authorization decisions.
Checkpoint 3 must turn reliable observations into historical access rules.
Until then, the read interface is for trusted administrative callers and tests.

## Table and keys

The table uses string `PK` and `SK` keys.
There are no DynamoDB secondary indexes and no automatic TTL deletion.
Search indexes are additional rows in the same table.
Each transaction writes an event and its search rows together.
This avoids a delay between an event write and its availability through a secondary index.

Let `N` be `H1|base64url(organization)|base64url(environment)`.
Each encoded value uses unpadded Base64 URL encoding.
Delimiters inside organization or environment values cannot merge namespaces.
Every key includes `N`, including identity lookups and search indexes.

| Record | PK | SK | Payload |
| --- | --- | --- | --- |
| Journey | `N\|J\|base64url(journey ID)` | `META` | Versioned journey JSON and numeric revision |
| Correlation | `N\|C\|kind\|base64url(exact ID)` | `LINK` | Target journey ID |
| Original event | `N\|E\|base64url(event ID)` | `EVENT` | Versioned event JSON |
| Search row | `N\|I\|dimension\|base64url(value)` | `UTC timestamp#event ID` | The same versioned event JSON |

Search dimensions are `all`, `journey`, `account-id`, `name`, `owner`, `actor`, and `event`.
The `all` dimension uses an empty value.
The adapter omits other empty optional dimensions.
Names, owners, and actors use Unicode simple case folding, matching `Filter.matches`.
Other IDs and event types use exact comparison.

The timestamp format is `2006-01-02T15:04:05.000000000Z`.
Fixed-width UTC timestamps preserve nanosecond order.
Raw event IDs provide deterministic ordering when timestamps match.
One event creates at most eight stored rows.
Its transaction also checks the journey revision to protect the account correlation read before the write.

The adapter limits identifiers to 256 UTF-8 bytes and JSON documents to 64 KiB.
A journey accepts at most 90 identity correlations.
These limits keep keys, items, and transactions below DynamoDB limits.
Oversized records fail explicitly rather than losing evidence.
Checkpoint 2 must store growing delivery work separately, rather than append every operation to the journey document.

## Search and errors

Search chooses the first supplied dimension in this order: journey, account ID, name, owner, actor, event type.
Without a supplied dimension, it uses `all`.
The date range narrows the sort key, with an inclusive start and exclusive end.
Other predicates filter the returned candidates.
Search continues across DynamoDB pages until it finds one extra match or exhausts the selected index.
There is no table scan, fixed candidate cap, or implicit time window.
Sparse combined filters can still require a long traversal of one index.

A cursor identifies a position in a search.
It binds the table, organization, environment, filters, and direction.
Page size can change between requests.
The cursor contains the last returned timestamp and event ID, not an offset.
New latest events do not shift subsequent pages.
Late events behind that position can appear later, but this traversal is not a frozen snapshot.
Cursors are not authorization credentials or tamper-proof tokens.

`Record` requires event and journey IDs.
A retry with the same ID and content succeeds without another displayed event.
The same ID with different content returns `ErrConflict`.
Events with account IDs require an exact account correlation in the journey.
This storage behavior does not yet guarantee delivery after producer loss.

A missing metadata record returns `ErrNotFound`.
A malformed record, unknown schema, or dangling correlation returns `ErrCorrupt`.
A failed DynamoDB operation returns `ErrUnavailable` and preserves the underlying error.
Search fails on corrupt rows rather than returning a successful partial page.
A later query failure also discards earlier candidates from that call.
A known journey without events returns an empty page, distinct from a missing journey.

`Incomplete: false` means that the query found no known storage gap.
It is not proof that every external action produced an event.
Arbitrary deletion of individual search rows is not detected by ordinary reads.
Checkpoint 2 must expose known delivery gaps and unresolved outcomes.
Checkpoint 5 must coordinate deletion across events, indexes, correlations, and authorization metadata.

## Tests and capacity limits

The default tests use an in-memory implementation of the adapter's SDK operations.
They force short DynamoDB pages and inject read failures, corruption, stale writes, and lost write responses.
The same storage contract also runs against an explicitly configured loopback DynamoDB endpoint.
That test creates and deletes only its own randomly named table.
It never loads AWS credentials or the default AWS configuration.

The loopback suite also starts a separate producer process.
After that process exits, the test removes its working directory.
A new client recovers a failed direct journey using exact operation and provider request IDs.
No queue record, account inventory, producer memory, or local retry file supplies the identity.
This proves recovery of accepted metadata, not recovery across an unrecorded AWS acceptance window.

To use an isolated DynamoDB Local container, start a new container:

```sh
container=$(docker run --detach --rm --publish 127.0.0.1::8000 \
  amazon/dynamodb-local:3.3.0 -jar DynamoDBLocal.jar -inMemory -sharedDb)
docker port "$container" 8000/tcp
```

Wait until the reported loopback endpoint accepts HTTP requests.
In the same shell, run the adapter tests:

```sh
PLAYPLACE_TEST_DYNAMODB_ENDPOINT="http://$(docker port "$container" 8000/tcp)" \
  go test -race ./internal/audit -count=1
```

After the tests, stop only this test container:

```sh
docker stop "$container"
```

Checkpoint 1 passed against `amazon/dynamodb-local:3.3.0` with the race detector.
The contract includes 304 events across two journeys, nanosecond timestamps, tied timestamps, and combined filters in both orders.
It also covers archived requests, closed accounts, transfers, exact joins, namespace isolation, and duplicate event delivery.
These tests establish behavior, not production capacity.

The selected indexing strategy targets 1,000 journeys and 100,000 events per organization per year.
The two-second first-page target remains unmeasured at that workload.
Checkpoint 6 must measure ordinary and sparse combined searches, retained open journeys, latency, storage cost, and throttling.
DynamoDB Local results do not establish AWS performance.
