/**
 * Maps `task` over `items` with at most `limit` calls in flight, resolving to
 * the results in input order.
 *
 * A bounded fan-out matters wherever one user action expands into N server
 * requests: `Promise.all(items.map(...))` starts every request at once, so a
 * large selection opens the whole burst against the API — and, for actions
 * that reach further (a reset talks to the user's torrent client), against
 * whatever the API talks to next.
 *
 * `task` is expected to handle its own failures. A rejection propagates and
 * abandons the remaining results, exactly as `Promise.all` does.
 */
export async function mapWithConcurrency<T, R>(
  items: T[],
  limit: number,
  task: (item: T, index: number) => Promise<R>,
): Promise<R[]> {
  const results = new Array<R>(items.length);
  let next = 0;
  // Each worker pulls the next index until the queue drains. `next++` needs no
  // lock: JS runs the read-increment-assign to completion before any await.
  const workers = Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (next < items.length) {
      const index = next++;
      results[index] = await task(items[index], index);
    }
  });
  await Promise.all(workers);
  return results;
}
