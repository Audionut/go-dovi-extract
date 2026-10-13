// Development-only oracle: compile against the pinned original source.
// DOVI_REFERENCE is set at compile time. No Go plotting code is involved.
use std::path::PathBuf;
mod commands {
    use super::*;
    pub struct InfoArgs { pub input: Option<PathBuf>, pub input_pos: Option<PathBuf>, pub frame: Option<usize>, pub summary: bool }
    pub struct PlotArgs {
        pub input: Option<PathBuf>, pub input_pos: Option<PathBuf>, pub output: Option<PathBuf>,
        pub title: Option<String>, pub start: Option<usize>, pub end: Option<usize>,
        pub plot_type: crate::dovi::plotter::PlotType, pub target_nits_str: String,
        pub trims: Option<Vec<crate::dovi::plotter::TrimParameter>>,
    }
}
mod dovi {
    pub fn input_from_either(_: &str, a: Option<std::path::PathBuf>, b: Option<std::path::PathBuf>) -> anyhow::Result<std::path::PathBuf> { Ok(a.or(b).unwrap()) }
    pub mod rpu_info { include!(concat!(env!("DOVI_REFERENCE"), "/src/dovi/rpu_info.rs")); }
    pub mod plotter {
        include!(concat!(env!("DOVI_REFERENCE"), "/src/dovi/plotter.rs"));
        pub fn reference(path: &str, start: usize, end: Option<usize>) -> anyhow::Result<serde_json::Value> {
            let rpus = parse_rpu_file(path)?;
            let rpus = &rpus[start..=end.unwrap_or(rpus.len()-1)];
            let s = RpusListSummary::new(rpus)?;
            // Plotters 0.3.7 loops forever for a descending positive-step range.
            // Do not invoke the upstream axis on <=100-nit or zero fixtures.
            let coord = if s.l1_stats.max_pq_value > nits_to_pq(100.0) {Some(PlotCoord::new(PlotType::L1, &s))} else {None};
            let stats = &s.l1_stats;
            let mut first = format!("Frames: {}. {}. Scenes: {}. DM version: {}.",s.count,s.profiles_str,s.scene_count,s.dm_version_str);
            if let Some((v1,v2))=s.dm_version_counts { first.push_str(&format!(" v2.9 count: {v1}, v4.0 count: {v2}")); }
            let mut second = s.rpu_mastering_meta_str.clone();
            if let Some(p)=&s.l9_mdp { if !p.is_empty() {second.push_str(&format!(" - {}",p.join(", ")));} }
            let l6=match &s.l6_meta {None=>"None".to_owned(),Some(v)=>if v.len()>2 {format!("{}. Total different: {}",v[..2].join(". "),v.len())}else{v.join(". ")}};
            let mut right=vec![format!("L5 offsets: {}",s.l5_str)];
            if !s.l2_trims.is_empty(){right.push(format!("L2 trims: {}",s.l2_trims.join(", ")));}
            if let Some(v)=&s.l8_trims {if !v.is_empty(){right.push(format!("L8 trims: {}",v.join(", ")));}}
            while right.len()<3{right.insert(0,String::new());}
            Ok(serde_json::json!({"Ceiling":stats.max_pq_value,"Stats":[stats.maxcll,stats.maxcll_avg,stats.maxfall,stats.maxfall_avg,stats.max_min_nits],"Ticks":coord.map(|c|c.key_points),"Left":[first,second,format!("L6 metadata: {l6}")],"Right":right}))
        }
    }
}
fn main() -> anyhow::Result<()> {
    let args:Vec<_>=std::env::args().collect();
    let start=args.get(2).map(|x|x.parse().unwrap()).unwrap_or(0);
    let end=args.get(3).map(|x|x.parse().unwrap());
    println!("{}",serde_json::to_string_pretty(&dovi::plotter::reference(&args[1],start,end)?)?);
    Ok(())
}
