export type ShippingAddressFields = {
  label?: string;
  recipientName: string;
  addressLine1: string;
  addressLine2?: string;
  city: string;
  region: string;
  postalCode: string;
  country: string;
  phone?: string;
};

export type ShippingAddressField = keyof ShippingAddressFields;
export type ShippingAddressErrors = Partial<Record<ShippingAddressField, string>>;

const postalCodePattern = /^[A-Za-z0-9][A-Za-z0-9 -]{1,15}$/;
// Shopify create_checkout이 실제로 거절하는 형식을 서버(NormalizeUSShippingAddress)와
// 같은 규칙으로 제출 전에 걸러 사용자에게 필드 단위로 즉시 알린다.
const usZIPPattern = /^\d{5}(-\d{4})?$/;
const usPhonePattern = /^\+?1?[2-9]\d{2}[2-9]\d{6}$/;
const usRegionCodes = new Set(
  ("AL AK AZ AR CA CO CT DE FL GA HI ID IL IN IA KS KY LA ME MD MA MI MN MS MO " +
    "MT NE NV NH NJ NM NY NC ND OH OK OR PA RI SC SD TN TX UT VT VA WA WV WI WY DC").split(" "),
);

export function normalizeUSPhoneInput(phone: string) {
  return phone.trim().replace(/[\s().-]/g, "");
}

export function formatUSPhoneInput(phone: string) {
  const normalized = normalizeUSPhoneInput(phone);
  const nationalNumber = normalized.startsWith("+1")
    ? normalized.slice(2)
    : normalized.length === 11 && normalized.startsWith("1")
      ? normalized.slice(1)
      : normalized;
  if (!/^\d{10}$/.test(nationalNumber)) return phone.trim();
  return `+1 ${nationalNumber.slice(0, 3)} ${nationalNumber.slice(3, 6)} ${nationalNumber.slice(6)}`;
}

export function validateShippingAddress(
  address: ShippingAddressFields,
  options: { requireUS?: boolean; requireLabel?: boolean } = {},
): ShippingAddressErrors {
  const errors: ShippingAddressErrors = {};
  required(errors, "recipientName", address.recipientName, localizeFixedCopy("Enter the recipient.", "수령인을 입력해 주세요."), 120);
  required(errors, "addressLine1", address.addressLine1, localizeFixedCopy("Enter address line 1.", "주소 1을 입력해 주세요."), 200);
  optionalMax(errors, "addressLine2", address.addressLine2, 200, localizeFixedCopy("Address line 2 must be 200 characters or fewer.", "주소 2는 200자 이하여야 합니다."));
  required(errors, "city", address.city, localizeFixedCopy("Enter the city.", "도시를 입력해 주세요."), 100);
  required(errors, "region", address.region, localizeFixedCopy("Enter the state or province.", "주(State)를 입력해 주세요."), 100);
  const country = address.country.trim().toUpperCase();
  if (!/^[A-Z]{2}$/.test(country)) {
    errors.country = localizeFixedCopy("Enter a 2-letter ISO country code.", "ISO 국가 코드 두 글자를 입력해 주세요.");
  } else if (options.requireUS && country !== "US") {
    errors.country = localizeFixedCopy("The current order sheet supports US shipping addresses only.", "현재 주문서는 미국(US) 배송지만 지원합니다.");
  }
  if (options.requireUS) {
    if (!errors.region && !usRegionCodes.has(address.region.trim().toUpperCase())) {
      errors.region = localizeFixedCopy("Enter a 2-letter US state code (for example, NY or CA).", "미국 주 코드 두 글자로 입력해 주세요. (예: NY, CA)");
    }
    if (!usZIPPattern.test(address.postalCode.trim())) {
      errors.postalCode = localizeFixedCopy("Enter a 5-digit US ZIP or ZIP+4 (for example, 10012 or 10012-1234).", "미국 ZIP 5자리 또는 ZIP+4 형식으로 입력해 주세요. (예: 10012 또는 10012-1234)");
    }
    if (!usPhonePattern.test(normalizeUSPhoneInput(address.phone ?? ""))) {
      errors.phone = localizeFixedCopy("Enter a US phone number in +1 format (for example, +1 202 555 0123).", "미국 전화번호를 +1 형식으로 입력해 주세요. (예: +1 202 555 0123)");
    }
  } else {
    if (!postalCodePattern.test(address.postalCode.trim())) {
      errors.postalCode = localizeFixedCopy("Enter a postal code with 2–16 letters, numbers, spaces, or hyphens.", "우편번호는 문자·숫자·공백·하이픈 2~16자로 입력해 주세요.");
    }
    optionalMax(errors, "phone", address.phone, 40, localizeFixedCopy("Phone number must be 40 characters or fewer.", "전화번호는 40자 이하여야 합니다."));
  }
  if (options.requireLabel) {
    required(errors, "label", address.label ?? "", localizeFixedCopy("Enter an address label.", "배송지 이름을 입력해 주세요."), 80);
  }
  return errors;
}

export function shippingAddressErrorSummary(errors: ShippingAddressErrors) {
  const count = Object.keys(errors).length;
  return count === 0 ? undefined : localizeFixedCopy(
    "{count} shipping-address fields need attention.",
    "확인이 필요한 배송지 항목이 {count}개 있습니다.",
    { count },
  );
}

function required(
  errors: ShippingAddressErrors,
  field: ShippingAddressField,
  value: string,
  message: string,
  max: number,
) {
  const normalized = value.trim();
  if (!normalized) errors[field] = message;
  else if (normalized.length > max) errors[field] = localizeFixedCopy(
    "Must be {max} characters or fewer.",
    "{max}자 이하여야 합니다.",
    { max },
  );
}

function optionalMax(
  errors: ShippingAddressErrors,
  field: ShippingAddressField,
  value: string | undefined,
  max: number,
  message: string,
) {
  if ((value ?? "").trim().length > max) errors[field] = message;
}
import { localizeFixedCopy } from "../../../shared/i18n";
