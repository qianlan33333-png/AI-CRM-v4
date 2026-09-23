package port

import productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"

// ProductReader is the canonical Product-owned target-validation port. Coupon
// does not own a product directory and may neither query Product tables nor
// import Product implementation packages.
type ProductReader = productport.ProductTargetReader
