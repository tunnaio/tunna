// Cuts a release by creating and pushing one tag (ADR-0011, ADR-0012). The
// tag is what releases: CI builds, publishes and attests from it.
//
//   bun scripts/release.ts server            next server prerelease, v0.1.0-alpha.N
//   bun scripts/release.ts sdk               next SDK prerelease: bump, commit, tag
//   bun scripts/release.ts server 0.1.0      an exact version instead of the next one
//   ... --dry-run                            print every step, change nothing
//
// It refuses unless the tree is clean, on main, level with origin, and CI is
// green on the commit, and it asks for the version to be typed before it
// creates anything. It never pushes with --tags: one tag, by name.

import { $ } from "bun";

$.throws(true);

const SDK_DIR = "sdk/typescript";
const PREFIX = { server: "v", sdk: "sdk/typescript/v" } as const;
type Line = keyof typeof PREFIX;

const args = process.argv.slice(2);
const dryRun = args.includes("--dry-run");
const skipCI = args.includes("--skip-ci-check");
const [line, exact] = args.filter((a) => !a.startsWith("--")) as [Line | undefined, string | undefined];

if (line !== "server" && line !== "sdk") {
  console.error("usage: bun scripts/release.ts <server|sdk> [version] [--dry-run] [--skip-ci-check]");
  process.exit(2);
}

const text = async (cmd: ReturnType<typeof $>) => (await cmd.quiet().text()).trim();
const fail = (why: string): never => {
  console.error(`\nrefused: ${why}`);
  process.exit(1);
};
// refuse is fail for the preconditions. A dry run reports them and goes on,
// so one run shows the whole plan and everything that would have stopped it.
let refusals = 0;
const refuse = (why: string): void => {
  if (!dryRun) fail(why);
  refusals++;
  console.log(`  would refuse: ${why}`);
};
// run executes a step that changes something, or prints it under --dry-run.
const run = async (what: string, cmd: () => ReturnType<typeof $>) => {
  console.log(`  ${dryRun ? "would run" : "running"}: ${what}`);
  if (!dryRun) await cmd();
};

const root = await text($`git rev-parse --show-toplevel`);
process.chdir(root);

// --- the state a release may start from ---

const branch = await text($`git rev-parse --abbrev-ref HEAD`);
if (branch !== "main") refuse(`on ${branch}; releases are cut from main`);

if ((await text($`git status --porcelain`)) !== "") refuse("the working tree has uncommitted changes");

await $`git fetch --quiet --tags origin`;
const head = await text($`git rev-parse HEAD`);
if (head !== (await text($`git rev-parse origin/main`))) refuse("main is not level with origin/main; push or pull first");

async function ciIsGreen(sha: string): Promise<void> {
  if (skipCI) return console.log("  CI check skipped by flag");
  const out = await text($`gh run list --workflow ci.yml --commit ${sha} --limit 1 --json status,conclusion`).catch(() => "");
  if (out === "") return refuse("could not ask GitHub for the CI run (is gh installed and signed in?); --skip-ci-check overrides");
  const [latest] = JSON.parse(out) as { status: string; conclusion: string }[];
  if (!latest) return refuse(`no CI run found for ${sha.slice(0, 7)}; wait for it, or --skip-ci-check`);
  if (latest.status !== "completed") return refuse(`CI is still ${latest.status} on ${sha.slice(0, 7)}`);
  if (latest.conclusion !== "success") return refuse(`CI concluded ${latest.conclusion} on ${sha.slice(0, 7)}`);
  console.log(`  CI is green on ${sha.slice(0, 7)}`);
}

// --- which version ---

const VERSION = /^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/;

// next is the version after the newest tag on this line: the last number of
// a prerelease goes up by one. A stable version has no obvious successor.
async function next(l: Line): Promise<string> {
  const newest = (await text($`git tag --list ${PREFIX[l] + "*"} --sort=-v:refname`)).split("\n")[0] ?? "";
  if (newest === "") fail(`no ${PREFIX[l]}* tag yet; give the version explicitly`);
  const current = newest.slice(PREFIX[l].length);
  const m = /^(.*\.)(\d+)$/.exec(current);
  if (!m || !current.includes("-")) fail(`${current} is not a prerelease; give the next version explicitly`);
  return `${m![1]}${Number(m![2]) + 1}`;
}

const version = exact ?? (await next(line));
if (!VERSION.test(version)) fail(`${JSON.stringify(version)} is not a version like 0.1.0 or 0.1.0-alpha.7`);
const tag = PREFIX[line] + version;
if ((await text($`git tag --list ${tag}`)) !== "") fail(`the tag ${tag} already exists`);

// --- the plan, and the one question ---

console.log(`\n${line} release ${version}${dryRun ? "  (dry run: nothing will change)" : ""}`);
console.log(`  commit  ${head.slice(0, 7)}  ${await text($`git log -1 --format=%s`)}`);
console.log(`  tag     ${tag}`);
await ciIsGreen(head);

if (!dryRun) {
  const typed = prompt(`\nType ${version} to release it:`);
  if (typed?.trim() !== version) fail("the version typed did not match; nothing was changed");
}
console.log("");

// --- the release ---

let tagged = head;
if (line === "sdk") {
  // The package version is the tag's version; the publish workflow refuses
  // a tag that disagrees with package.json.
  const pkgPath = `${SDK_DIR}/package.json`;
  await run(`set "version": "${version}" in ${pkgPath}`, async () => {
    const pkg = await Bun.file(pkgPath).text();
    const bumped = pkg.replace(/("version":\s*")[^"]+(")/, `$1${version}$2`);
    if (bumped === pkg) fail(`${pkgPath} already says ${version}, or has no version field`);
    await Bun.write(pkgPath, bumped);
    return $`true`;
  });
  await run(`git commit -m "sdk/typescript ${version}" -- ${pkgPath}`, () => $`git commit --quiet -m ${"sdk/typescript " + version} -- ${pkgPath}`);
  await run("git push origin main", () => $`git push --quiet origin main`);
  if (!dryRun) tagged = await text($`git rev-parse HEAD`);
}

await run(`git tag ${tag} ${dryRun && line === "sdk" ? "<the bump commit>" : tagged.slice(0, 7)}`, () => $`git tag ${tag} ${tagged}`);
await run(`git push origin ${tag}`, () => $`git push --quiet origin ${tag}`);

// --- what is left, which only a person can do ---

if (dryRun && refusals > 0) console.log(`\n${refusals} precondition${refusals === 1 ? "" : "s"} would have stopped a real run.`);
console.log(`\n${dryRun ? "After a real run:" : "Tagged and pushed."}`);
if (line === "server") {
  console.log(`  watch    gh run watch $(gh run list --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')`);
  console.log(`  image    ghcr.io/tunnaio/tunna:${version}`);
  console.log("  then     redeploy, and move the pinned image in README.md and docs/deploy.md");
} else {
  console.log("  watch    gh run watch $(gh run list --workflow publish-typescript.yml --limit 1 --json databaseId --jq '.[0].databaseId')");
  console.log("  approve  the staged version on npmjs.com (2FA)");
  console.log(`  then     npm dist-tag add tunna@${version} latest`);
}
