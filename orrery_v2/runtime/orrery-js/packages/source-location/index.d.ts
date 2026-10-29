export interface Options { offsetLine?: number; offsetColumn?: number; startIndex?: number }
export interface Location { line: number; column: number; character: number }
export function getLocator(source: string, options?: Options): (search: string | number, index?: number) => Location | undefined;
export function locate(source: string, search: string | number, options?: Options): Location | undefined;
