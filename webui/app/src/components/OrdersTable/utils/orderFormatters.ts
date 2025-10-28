/**
 * Formats a status value into a readable label
 */
export const formatStatusLabel = (value?: string): string => {
  if (!value) return 'Pending';

  return value
    .toString()
    .replace(/_/g, ' ')
    .toLowerCase()
    .replace(/\b\w/g, (char) => char.toUpperCase());
};

/**
 * Formats a date string for display
 */
export const formatDate = (date?: string): string => {
  if (!date) {
    return '—';
  }

  const parsed = new Date(date);
  if (Number.isNaN(parsed.getTime())) {
    return '—';
  }

  return parsed.toLocaleString();
};

/**
 * Formats a price value for display
 */
export const formatPrice = (value: number): string =>
  value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 6,
  });

/**
 * Formats a quantity value for display
 */
export const formatQuantity = (value: number): string =>
  value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 8,
  });
