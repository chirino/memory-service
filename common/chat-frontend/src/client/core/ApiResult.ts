/* eslint-disable @typescript-eslint/no-explicit-any */
export type ApiResult<TData = any> = {
  readonly body: TData;
  readonly ok: boolean;
  readonly status: number;
  readonly statusText: string;
  readonly url: string;
};
