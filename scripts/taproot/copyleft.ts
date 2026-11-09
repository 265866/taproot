// What a package's license obliges taproot to do, for third-party-notices.ts.

// 'none': nothing beyond passing on the notice. 'lgpl': the GNU LGPL v3,
// which the notices handle for Rust crates. 'other': a copyleft license the
// notices do not handle, strong or weak, such as the GPL, the AGPL, an older
// LGPL, the MPL, or the EPL.
export type Copyleft = 'none' | 'lgpl' | 'other';

const RANK: Copyleft[] = ['none', 'lgpl', 'other'];

// SPDX license ids, by prefix, that attach source-availability or
// same-license duties to a binary that includes the code. Every GNU license
// matches /GPL/ on its own.
const COPYLEFT =
  /GPL|^(MPL|EPL|CDDL|EUPL|OSL|CC-BY-SA|SSPL|CPL|CPAL|MS-RL|CECILL|APSL|RPL|RPSL|QPL|ODbL|LiLiQ-R|Sleepycat)(-|$)/i;

// copyleft evaluates an SPDX license expression, taking the cheapest
// alternative of each OR and the strictest part of each AND. It throws on an
// expression it cannot parse, so that a new kind of license gets a person's
// look instead of a guess.
export function copyleft(expression: string): Copyleft {
  const tokens = expression.match(/\(|\)|[^\s()]+/g) ?? [];
  let pos = 0;
  const fail = (): never => {
    throw new Error(`cannot parse license expression: ${expression}`);
  };
  // expr := term (OR term)*; term := factor (AND factor)*;
  // factor := '(' expr ')' | id [WITH id]
  const expr = (): Copyleft => {
    let best = term();
    while (tokens[pos]?.toUpperCase() === 'OR') {
      pos++;
      const next = term();
      if (RANK.indexOf(next) < RANK.indexOf(best)) best = next;
    }
    return best;
  };
  const term = (): Copyleft => {
    let worst = factor();
    while (tokens[pos]?.toUpperCase() === 'AND') {
      pos++;
      const next = factor();
      if (RANK.indexOf(next) > RANK.indexOf(worst)) worst = next;
    }
    return worst;
  };
  const factor = (): Copyleft => {
    const token = tokens[pos++];
    if (token === undefined || token === ')' || /^(AND|OR|WITH)$/i.test(token)) return fail();
    if (token === '(') {
      const inner = expr();
      if (tokens[pos++] !== ')') fail();
      return inner;
    }
    if (tokens[pos]?.toUpperCase() === 'WITH') {
      // An exception adds permissions but is not one the notices know.
      if (tokens[pos + 1] === undefined) fail();
      pos += 2;
      return COPYLEFT.test(token) ? 'other' : 'none';
    }
    if (/^LGPL-3\.0(-only|-or-later|\+)?$/i.test(token)) return 'lgpl';
    return COPYLEFT.test(token) ? 'other' : 'none';
  };
  const result = expr();
  if (pos !== tokens.length) fail();
  return result;
}
