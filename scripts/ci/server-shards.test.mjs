import test from 'node:test';
import assert from 'node:assert/strict';
import { partition, shardFor } from './server-shards.mjs';
const p = 'github.com/vitlane/vitlane/server/';
test('partitions all packages exactly once, including new packages and shared migration tests', () => {
  const paths = ['cmd/vitlane', 'internal/account/infra/postgres', 'internal/curation/research/app', 'internal/ordering/payment/giwa/infra/postgres', 'internal/ordering/process/infra/postgres', 'internal/shared/infra/postgres', 'internal/future/domain'];
  const input = paths.map(x => p + x);
  assert.deepEqual(Object.values(partition(input)).flat().sort(), [...input].sort());
  assert.equal(shardFor(p + 'internal/shared/infra/postgres'), 'core');
  assert.equal(shardFor(p + 'internal/ordering/payment/giwa/infra/postgres'), 'payment');
  assert.throws(() => partition([]));
  assert.throws(() => partition([...input, input[0]]));
  assert.throws(() => shardFor('unrelated/module'));
});
