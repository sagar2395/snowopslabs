// SPDX-License-Identifier: Apache-2.0

/** errMessage normalises an unknown thrown value (TanStack Query typs errors as
 *  unknown) into a human string for the designed error states. */
export function errMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}
