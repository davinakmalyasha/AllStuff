import { Coffee, ChefHat, Flame, Croissant, Martini, Scissors, Hand, Flower, Camera, Aperture, Music, Calendar, Shirt, Palette, Cpu, Car, Home, Printer, Dumbbell, Stethoscope, Leaf, Tag, type LucideIcon } from 'lucide-react'

/** Category icon registry (PRD §7.1 categories.icon). */
export const CATEGORY_ICONS: Record<string, LucideIcon> = {
  'utensils': ChefHat, 'coffee': Coffee, 'chef-hat': ChefHat, 'flame': Flame, 'croissant': Croissant, 'martini': Martini,
  'sparkles': Hand, 'scissors': Scissors, 'hand': Hand, 'flower': Flower,
  'camera': Camera, 'aperture': Aperture, 'music': Music, 'calendar': Calendar,
  'shopping-bag': Shirt, 'shirt': Shirt, 'palette': Palette, 'cpu': Cpu,
  'wrench': Car, 'car': Car, 'home': Home, 'printer': Printer,
  'heart-pulse': Dumbbell, 'dumbbell': Dumbbell, 'stethoscope': Stethoscope, 'leaf': Leaf,
}

export function categoryIcon(key?: string | null, fallback: LucideIcon = Tag): LucideIcon {
  return (key && CATEGORY_ICONS[key]) || fallback
}
