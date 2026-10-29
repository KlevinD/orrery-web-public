export interface ApiCollection<T> {
  items: T[];
}

export interface PageDTO {
  total?: number;
  limit?: number;
  offset?: number;
  returned?: number;
  has_more?: boolean;
}
