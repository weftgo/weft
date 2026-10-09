// The transcript editor's compaction fixture (plan F2 review), shared
// by the run page's and the panel's tests — kept out of src/panel,
// whose sources may spell no hole's words (badges.test.ts).
/** Step 2's request carried a compaction view: messages [1, 3) — c1's
 * call and result — replaced by one summary (transcript?step=2). */
export const AS_OF_2 = { step: 2, messages: [], compacted_at: { index: 0, step: 2, from_seq: 1, to_seq: 3, hash: "h", replaced: 2, entries: 1 } }
export const COMPACTED_C1 =
  'call "c1" of step 0 was compacted away before step 2\'s request (messages [1, 3) replaced by 1): the model never saw it there; edit from an earlier from_step'
