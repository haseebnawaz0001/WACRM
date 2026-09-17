import { cleanupE2EData } from './global-cleanup'

/**
 * Removes the suite's own rows when it finishes.
 *
 * The cleanup has always existed and only ran at the start of the next run, so
 * a suite pointed at a development database left its contacts, tasks and
 * conversations sitting in the product until somebody happened to run the
 * tests again. That is fine on the disposable database CI creates and wrong
 * everywhere else — the first thing a developer sees after running the tests
 * is forty test contacts in their contact list.
 *
 * Running at both ends is deliberate: starting clean still matters, because a
 * run killed halfway through never reaches its teardown.
 */
export default async function globalTeardown() {
  // The same default the setup uses, so the run that created the rows and the
  // run that removes them always agree about which database that was.
  const dbURL = process.env.TEST_DATABASE_URL || 'postgres://whatomate:whatomate@127.0.0.1:5432/whatomate'
  console.log('🧹 Global Teardown: removing this run\'s E2E data...')
  await cleanupE2EData(dbURL)
}
