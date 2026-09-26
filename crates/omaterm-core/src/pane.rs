use std::collections::HashSet;

use crate::{CoreError, PaneId, Result, SessionId, SplitId};

const MIN_FRACTION: f32 = 0.1;
const MAX_FRACTION: f32 = 0.9;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SplitAxis {
    Horizontal,
    Vertical,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SplitDirection {
    Left,
    Right,
    Up,
    Down,
}

impl SplitDirection {
    fn axis(self) -> SplitAxis {
        match self {
            Self::Left | Self::Right => SplitAxis::Horizontal,
            Self::Up | Self::Down => SplitAxis::Vertical,
        }
    }

    fn new_pane_is_first(self) -> bool {
        matches!(self, Self::Left | Self::Up)
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PaneContent {
    Empty,
    Terminal(SessionId),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Pane {
    pub id: PaneId,
    pub content: PaneContent,
}

impl Pane {
    pub fn empty() -> Self {
        Self {
            id: PaneId::new(),
            content: PaneContent::Empty,
        }
    }
}

#[derive(Debug, Clone, PartialEq)]
pub enum PaneNode {
    Pane(Pane),
    Split {
        id: SplitId,
        axis: SplitAxis,
        fraction: f32,
        first: Box<PaneNode>,
        second: Box<PaneNode>,
    },
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct NormalizedRect {
    pub x: f32,
    pub y: f32,
    pub width: f32,
    pub height: f32,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct PaneRect {
    pub pane: PaneId,
    pub rect: NormalizedRect,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Removal {
    pub pane: Pane,
}

#[derive(Debug, Clone, PartialEq)]
pub struct PaneTree {
    root: Option<PaneNode>,
}

impl PaneTree {
    pub fn new(pane: Pane) -> Self {
        Self {
            root: Some(PaneNode::Pane(pane)),
        }
    }

    pub fn from_root(root: PaneNode) -> Result<Self> {
        let tree = Self { root: Some(root) };
        tree.validate()?;
        Ok(tree)
    }

    pub fn root(&self) -> Option<&PaneNode> {
        self.root.as_ref()
    }

    pub fn is_empty(&self) -> bool {
        self.root.is_none()
    }

    pub fn split(
        &mut self,
        target: PaneId,
        direction: SplitDirection,
        new_pane: Pane,
    ) -> Result<()> {
        self.validate()?;
        if self.find(new_pane.id).is_some() {
            return Err(CoreError::DuplicatePaneId(new_pane.id));
        }
        let root = self.root.as_mut().ok_or(CoreError::EmptyTree)?;
        if !root.split(target, direction, new_pane) {
            return Err(CoreError::PaneNotFound(target));
        }
        Ok(())
    }

    pub fn remove(&mut self, target: PaneId) -> Result<Removal> {
        self.validate()?;
        if self.find(target).is_none() {
            return Err(CoreError::PaneNotFound(target));
        }
        let root = self.root.take().ok_or(CoreError::EmptyTree)?;
        let (root, pane) = root
            .remove(target)
            .expect("target was checked before removal");
        self.root = root;
        Ok(Removal { pane })
    }

    pub fn resize(&mut self, split: SplitId, fraction: f32) -> Result<()> {
        if !fraction.is_finite() {
            return Err(CoreError::NonFiniteFraction);
        }
        self.validate()?;
        let root = self.root.as_mut().ok_or(CoreError::EmptyTree)?;
        if !root.resize(split, fraction.clamp(MIN_FRACTION, MAX_FRACTION)) {
            return Err(CoreError::SplitNotFound(split));
        }
        Ok(())
    }

    pub fn split_fraction(&self, split: SplitId) -> Option<f32> {
        self.root.as_ref()?.split_fraction(split)
    }

    pub fn find(&self, target: PaneId) -> Option<&Pane> {
        self.root.as_ref()?.find(target)
    }

    pub fn find_mut(&mut self, target: PaneId) -> Option<&mut Pane> {
        self.root.as_mut()?.find_mut(target)
    }

    pub fn panes(&self) -> Vec<&Pane> {
        let mut panes = Vec::new();
        if let Some(root) = &self.root {
            root.panes(&mut panes);
        }
        panes
    }

    pub fn ancestors(&self, target: PaneId) -> Option<Vec<SplitId>> {
        let mut ancestors = Vec::new();
        self.root
            .as_ref()?
            .ancestors(target, &mut ancestors)
            .then_some(ancestors)
    }

    pub fn pane_rects(&self) -> Vec<PaneRect> {
        let mut panes = Vec::new();
        if let Some(root) = &self.root {
            root.pane_rects(
                NormalizedRect {
                    x: 0.0,
                    y: 0.0,
                    width: 1.0,
                    height: 1.0,
                },
                &mut panes,
            );
        }
        panes
    }

    pub fn neighbor(&self, from: PaneId, direction: SplitDirection) -> Option<PaneId> {
        let panes = self.pane_rects();
        let source = panes.iter().find(|pane| pane.pane == from)?.rect;
        panes
            .into_iter()
            .filter(|candidate| {
                candidate.pane != from && is_in_direction(source, candidate.rect, direction)
            })
            .filter(|candidate| overlaps_perpendicular(source, candidate.rect, direction))
            .min_by(|left, right| {
                let left = neighbor_key(source, left.rect, direction);
                let right = neighbor_key(source, right.rect, direction);
                left.0
                    .total_cmp(&right.0)
                    .then_with(|| left.1.total_cmp(&right.1))
            })
            .map(|candidate| candidate.pane)
    }

    pub fn equalize(&mut self) {
        if let Some(root) = &mut self.root {
            root.equalize();
        }
    }

    fn validate(&self) -> Result<()> {
        let mut panes = HashSet::new();
        let mut splits = HashSet::new();
        if let Some(root) = &self.root {
            root.validate(&mut panes, &mut splits)?;
        }
        Ok(())
    }
}

impl PaneNode {
    fn split(&mut self, target: PaneId, direction: SplitDirection, new_pane: Pane) -> bool {
        match self {
            Self::Pane(pane) if pane.id == target => {
                let existing = Self::Pane(pane.clone());
                let new = Self::Pane(new_pane);
                let (first, second) = if direction.new_pane_is_first() {
                    (new, existing)
                } else {
                    (existing, new)
                };
                *self = Self::Split {
                    id: SplitId::new(),
                    axis: direction.axis(),
                    fraction: 0.5,
                    first: Box::new(first),
                    second: Box::new(second),
                };
                true
            }
            Self::Pane(_) => false,
            Self::Split { first, second, .. } => {
                first.split(target, direction, new_pane.clone())
                    || second.split(target, direction, new_pane)
            }
        }
    }

    fn remove(self, target: PaneId) -> Option<(Option<Self>, Pane)> {
        match self {
            Self::Pane(pane) if pane.id == target => Some((None, pane)),
            Self::Pane(_) => None,
            Self::Split {
                id,
                axis,
                fraction,
                first,
                second,
            } => {
                if first.find(target).is_some() {
                    let (first, pane) = first.remove(target).expect("target is in the first child");
                    return Some((
                        Some(collapse(id, axis, fraction, first, Some(*second))),
                        pane,
                    ));
                }
                let (second, pane) = second.remove(target)?;
                Some((
                    Some(collapse(id, axis, fraction, Some(*first), second)),
                    pane,
                ))
            }
        }
    }

    fn resize(&mut self, target: SplitId, fraction: f32) -> bool {
        match self {
            Self::Pane(_) => false,
            Self::Split {
                id,
                fraction: current,
                first,
                second,
                ..
            } => {
                if *id == target {
                    *current = fraction;
                    true
                } else {
                    first.resize(target, fraction) || second.resize(target, fraction)
                }
            }
        }
    }

    fn split_fraction(&self, target: SplitId) -> Option<f32> {
        match self {
            Self::Pane(_) => None,
            Self::Split {
                id,
                fraction,
                first,
                second,
                ..
            } => {
                if *id == target {
                    Some(*fraction)
                } else {
                    first
                        .split_fraction(target)
                        .or_else(|| second.split_fraction(target))
                }
            }
        }
    }

    fn find(&self, target: PaneId) -> Option<&Pane> {
        match self {
            Self::Pane(pane) if pane.id == target => Some(pane),
            Self::Pane(_) => None,
            Self::Split { first, second, .. } => first.find(target).or_else(|| second.find(target)),
        }
    }

    fn find_mut(&mut self, target: PaneId) -> Option<&mut Pane> {
        match self {
            Self::Pane(pane) if pane.id == target => Some(pane),
            Self::Pane(_) => None,
            Self::Split { first, second, .. } => {
                first.find_mut(target).or_else(|| second.find_mut(target))
            }
        }
    }

    fn panes<'a>(&'a self, panes: &mut Vec<&'a Pane>) {
        match self {
            Self::Pane(pane) => panes.push(pane),
            Self::Split { first, second, .. } => {
                first.panes(panes);
                second.panes(panes);
            }
        }
    }

    fn ancestors(&self, target: PaneId, ancestors: &mut Vec<SplitId>) -> bool {
        match self {
            Self::Pane(pane) => pane.id == target,
            Self::Split {
                id, first, second, ..
            } => {
                ancestors.push(*id);
                if first.ancestors(target, ancestors) || second.ancestors(target, ancestors) {
                    true
                } else {
                    ancestors.pop();
                    false
                }
            }
        }
    }

    fn pane_rects(&self, rect: NormalizedRect, panes: &mut Vec<PaneRect>) {
        match self {
            Self::Pane(pane) => panes.push(PaneRect {
                pane: pane.id,
                rect,
            }),
            Self::Split {
                axis,
                fraction,
                first,
                second,
                ..
            } => {
                let (first_rect, second_rect) = split_rect(rect, *axis, *fraction);
                first.pane_rects(first_rect, panes);
                second.pane_rects(second_rect, panes);
            }
        }
    }

    fn equalize(&mut self) {
        if let Self::Split {
            fraction,
            first,
            second,
            ..
        } = self
        {
            *fraction = 0.5;
            first.equalize();
            second.equalize();
        }
    }

    fn validate(&self, panes: &mut HashSet<PaneId>, splits: &mut HashSet<SplitId>) -> Result<()> {
        match self {
            Self::Pane(pane) if panes.insert(pane.id) => Ok(()),
            Self::Pane(pane) => Err(CoreError::DuplicatePaneId(pane.id)),
            Self::Split {
                id,
                fraction,
                first,
                second,
                ..
            } => {
                if !splits.insert(*id) {
                    return Err(CoreError::DuplicateSplitId(*id));
                }
                if !fraction.is_finite() {
                    return Err(CoreError::NonFiniteFraction);
                }
                if !(MIN_FRACTION..=MAX_FRACTION).contains(fraction) {
                    return Err(CoreError::FractionOutOfBounds);
                }
                first.validate(panes, splits)?;
                second.validate(panes, splits)
            }
        }
    }
}

fn collapse(
    id: SplitId,
    axis: SplitAxis,
    fraction: f32,
    first: Option<PaneNode>,
    second: Option<PaneNode>,
) -> PaneNode {
    match (first, second) {
        (Some(first), Some(second)) => PaneNode::Split {
            id,
            axis,
            fraction,
            first: Box::new(first),
            second: Box::new(second),
        },
        (Some(node), None) | (None, Some(node)) => node,
        (None, None) => unreachable!("a binary split cannot lose both children in one removal"),
    }
}

fn split_rect(
    rect: NormalizedRect,
    axis: SplitAxis,
    fraction: f32,
) -> (NormalizedRect, NormalizedRect) {
    match axis {
        SplitAxis::Horizontal => (
            NormalizedRect {
                width: rect.width * fraction,
                ..rect
            },
            NormalizedRect {
                x: rect.x + rect.width * fraction,
                width: rect.width * (1.0 - fraction),
                ..rect
            },
        ),
        SplitAxis::Vertical => (
            NormalizedRect {
                height: rect.height * fraction,
                ..rect
            },
            NormalizedRect {
                y: rect.y + rect.height * fraction,
                height: rect.height * (1.0 - fraction),
                ..rect
            },
        ),
    }
}

fn is_in_direction(
    source: NormalizedRect,
    candidate: NormalizedRect,
    direction: SplitDirection,
) -> bool {
    match direction {
        SplitDirection::Left => candidate.x + candidate.width <= source.x,
        SplitDirection::Right => candidate.x >= source.x + source.width,
        SplitDirection::Up => candidate.y + candidate.height <= source.y,
        SplitDirection::Down => candidate.y >= source.y + source.height,
    }
}

fn overlaps_perpendicular(
    source: NormalizedRect,
    candidate: NormalizedRect,
    direction: SplitDirection,
) -> bool {
    match direction {
        SplitDirection::Left | SplitDirection::Right => {
            candidate.y < source.y + source.height && source.y < candidate.y + candidate.height
        }
        SplitDirection::Up | SplitDirection::Down => {
            candidate.x < source.x + source.width && source.x < candidate.x + candidate.width
        }
    }
}

fn neighbor_key(
    source: NormalizedRect,
    candidate: NormalizedRect,
    direction: SplitDirection,
) -> (f32, f32) {
    let (edge_distance, center_distance) = match direction {
        SplitDirection::Left => (
            source.x - (candidate.x + candidate.width),
            center(source.y, source.height) - center(candidate.y, candidate.height),
        ),
        SplitDirection::Right => (
            candidate.x - (source.x + source.width),
            center(source.y, source.height) - center(candidate.y, candidate.height),
        ),
        SplitDirection::Up => (
            source.y - (candidate.y + candidate.height),
            center(source.x, source.width) - center(candidate.x, candidate.width),
        ),
        SplitDirection::Down => (
            candidate.y - (source.y + source.height),
            center(source.x, source.width) - center(candidate.x, candidate.width),
        ),
    };
    (edge_distance, center_distance.abs())
}

fn center(start: f32, size: f32) -> f32 {
    start + size / 2.0
}

#[cfg(test)]
mod tests {
    use super::*;

    fn pane() -> Pane {
        Pane::empty()
    }

    #[test]
    fn splits_all_directions_in_the_required_order() {
        for (direction, axis, new_first) in [
            (SplitDirection::Left, SplitAxis::Horizontal, true),
            (SplitDirection::Right, SplitAxis::Horizontal, false),
            (SplitDirection::Up, SplitAxis::Vertical, true),
            (SplitDirection::Down, SplitAxis::Vertical, false),
        ] {
            let original = pane();
            let added = pane();
            let mut tree = PaneTree::new(original.clone());
            tree.split(original.id, direction, added.clone()).unwrap();
            let PaneNode::Split {
                axis: actual_axis,
                first,
                second,
                ..
            } = tree.root().unwrap()
            else {
                panic!("expected split")
            };
            assert_eq!(*actual_axis, axis);
            assert!(
                first
                    .find(if new_first { added.id } else { original.id })
                    .is_some()
            );
            assert!(
                second
                    .find(if new_first { original.id } else { added.id })
                    .is_some()
            );
        }
    }

    #[test]
    fn nested_split_only_replaces_the_target_leaf() {
        let first = pane();
        let second = pane();
        let third = pane();
        let mut tree = PaneTree::new(first.clone());
        tree.split(first.id, SplitDirection::Down, second.clone())
            .unwrap();
        tree.split(second.id, SplitDirection::Right, third.clone())
            .unwrap();
        let PaneNode::Split {
            first: top,
            second: bottom,
            ..
        } = tree.root().unwrap()
        else {
            panic!()
        };
        assert_eq!(top.find(first.id).unwrap().id, first.id);
        assert!(matches!(
            bottom.as_ref(),
            PaneNode::Split {
                axis: SplitAxis::Horizontal,
                ..
            }
        ));
        assert_eq!(
            tree.panes().iter().map(|pane| pane.id).collect::<Vec<_>>(),
            vec![first.id, second.id, third.id]
        );
    }

    #[test]
    fn remove_collapses_parent_and_allows_empty_root() {
        let first = pane();
        let second = pane();
        let mut tree = PaneTree::new(first.clone());
        tree.split(first.id, SplitDirection::Right, second.clone())
            .unwrap();
        assert_eq!(tree.remove(second.id).unwrap().pane, second);
        assert_eq!(tree.panes(), vec![&first]);
        assert_eq!(tree.remove(first.id).unwrap().pane, first);
        assert!(tree.is_empty());
    }

    #[test]
    fn failed_operations_leave_tree_unchanged() {
        let first = pane();
        let missing = PaneId::new();
        let mut tree = PaneTree::new(first.clone());
        let before = tree.clone();
        assert_eq!(tree.remove(missing), Err(CoreError::PaneNotFound(missing)));
        assert_eq!(tree, before);
        assert_eq!(
            tree.split(first.id, SplitDirection::Right, first.clone()),
            Err(CoreError::DuplicatePaneId(first.id))
        );
        assert_eq!(tree, before);
    }

    #[test]
    fn resize_clamps_and_rejects_non_finite_values() {
        let first = pane();
        let second = pane();
        let mut tree = PaneTree::new(first.clone());
        tree.split(first.id, SplitDirection::Right, second).unwrap();
        let PaneNode::Split { id, .. } = tree.root().unwrap() else {
            panic!()
        };
        let id = *id;
        tree.resize(id, 2.0).unwrap();
        let PaneNode::Split { fraction, .. } = tree.root().unwrap() else {
            panic!()
        };
        assert_eq!(*fraction, MAX_FRACTION);
        assert_eq!(tree.resize(id, f32::NAN), Err(CoreError::NonFiniteFraction));
    }

    #[test]
    fn malformed_roots_are_rejected_before_mutation() {
        let first = pane();
        let second = pane();
        let invalid = PaneNode::Split {
            id: SplitId::new(),
            axis: SplitAxis::Horizontal,
            fraction: 0.95,
            first: Box::new(PaneNode::Pane(first)),
            second: Box::new(PaneNode::Pane(second)),
        };
        assert_eq!(
            PaneTree::from_root(invalid),
            Err(CoreError::FractionOutOfBounds)
        );
    }

    #[test]
    fn ancestors_include_root_to_parent_and_missing_is_none() {
        let first = pane();
        let second = pane();
        let third = pane();
        let mut tree = PaneTree::new(first.clone());
        tree.split(first.id, SplitDirection::Down, second.clone())
            .unwrap();
        let outer = match tree.root().unwrap() {
            PaneNode::Split { id, .. } => *id,
            _ => panic!(),
        };
        tree.split(second.id, SplitDirection::Right, third.clone())
            .unwrap();
        let inner = match tree.root().unwrap() {
            PaneNode::Split { second, .. } => match second.as_ref() {
                PaneNode::Split { id, .. } => *id,
                _ => panic!(),
            },
            _ => panic!(),
        };
        assert_eq!(tree.ancestors(third.id), Some(vec![outer, inner]));
        assert_eq!(tree.ancestors(PaneId::new()), None);
    }

    #[test]
    fn neighbor_uses_geometry_then_traversal_order() {
        let a = pane();
        let b = pane();
        let c = pane();
        let d = pane();
        let mut tree = PaneTree::new(a.clone());
        tree.split(a.id, SplitDirection::Right, b.clone()).unwrap();
        tree.split(a.id, SplitDirection::Down, c.clone()).unwrap();
        tree.split(b.id, SplitDirection::Down, d.clone()).unwrap();
        assert_eq!(tree.neighbor(a.id, SplitDirection::Right), Some(b.id));
        assert_eq!(tree.neighbor(c.id, SplitDirection::Right), Some(d.id));
        assert_eq!(tree.neighbor(a.id, SplitDirection::Left), None);
    }

    #[test]
    fn equalize_resets_every_split() {
        let first = pane();
        let second = pane();
        let third = pane();
        let mut tree = PaneTree::new(first.clone());
        tree.split(first.id, SplitDirection::Right, second.clone())
            .unwrap();
        let outer = match tree.root().unwrap() {
            PaneNode::Split { id, .. } => *id,
            _ => panic!(),
        };
        tree.split(second.id, SplitDirection::Down, third).unwrap();
        tree.resize(outer, 0.8).unwrap();
        tree.equalize();
        fn fractions(node: &PaneNode, values: &mut Vec<f32>) {
            if let PaneNode::Split {
                fraction,
                first,
                second,
                ..
            } = node
            {
                values.push(*fraction);
                fractions(first, values);
                fractions(second, values);
            }
        }
        let mut values = Vec::new();
        fractions(tree.root().unwrap(), &mut values);
        assert_eq!(values, vec![0.5, 0.5]);
    }
}
