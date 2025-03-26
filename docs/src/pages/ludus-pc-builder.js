// src/pages/ludus-pc-builder.js
import React, { useState, useEffect, useMemo } from 'react';

// Import the component data
import componentData from '@site/static/data/pc_components.json';

import styles from './ludus-pc-builder.module.css'; // We'll create this CSS file

// --- Helper Functions ---
function findCpuByName(name) {
    if (!name) return null;
    // Case-insensitive search
    const searchTerm = name.toLowerCase();
    return componentData.cpu.find(cpu => cpu.name.toLowerCase() === searchTerm) || { name: name }; // Return a stub if not found
  }
  
  function formatSizeTB(gb) {
    if (gb == null || gb < 0) return 'N/A'; // Handle null/undefined/negative
    if (gb === 0) return '0 TB';
    const tb = gb / 1000;
    return `${tb.toFixed(1)} TB`;
  }
  
  // --- Main Component ---
  function MiniPcBuilder() {
    // --- State ---
    const [selectedChassisId, setSelectedChassisId] = useState('');
    const [selectedCpuId, setSelectedCpuId] = useState('');
    const [selectedRamId, setSelectedRamId] = useState('');
    const [selectedDiskIds, setSelectedDiskIds] = useState([]);
  
    // --- Derived State & Memos ---
    const selectedChassis = useMemo(() =>
      componentData.chassis.find(c => c.id === selectedChassisId),
      [selectedChassisId]
    );
  
    // Booleans to check for included components
    const isCpuIncluded = useMemo(() => !!selectedChassis?.cpu_included, [selectedChassis]);
    const isRamIncluded = useMemo(() => selectedChassis?.ram_included != null, [selectedChassis]); // Check for non-null value
    const isDiskIncluded = useMemo(() => selectedChassis?.disk_included != null, [selectedChassis]); // Check for non-null value
  
    // Find the full CPU object if it's included
    const includedCpuObject = useMemo(() =>
      isCpuIncluded ? findCpuByName(selectedChassis.cpu_included) : null,
      [selectedChassis, isCpuIncluded]
    );
  
    // Determine the CPU being used (either included or selected)
    const effectiveCpu = useMemo(() =>
      includedCpuObject || componentData.cpu.find(c => c.id === selectedCpuId),
      [includedCpuObject, selectedCpuId]
    );
  
    // Get selected RAM (only relevant if RAM is not included)
    const selectedRam = useMemo(() =>
      !isRamIncluded ? componentData.ram.find(r => r.id === selectedRamId) : null,
      [selectedRamId, isRamIncluded]
    );
  
    // Get selected Disks (only relevant if Disk is not included)
    const selectedDisks = useMemo(() =>
      !isDiskIncluded ? selectedDiskIds.map(id => componentData.disk.find(d => d.id === id)).filter(Boolean) : [],
      [selectedDiskIds, isDiskIncluded]
    );
  
    // Max disks allowed *to be added*
    const maxDisksToAdd = useMemo(() =>
      isDiskIncluded ? 0 : (selectedChassis ? selectedChassis.number_of_disks : 0),
      [selectedChassis, isDiskIncluded]
    );
  
    // --- Effects ---
    useEffect(() => {
      if (selectedChassis) {
        // Reset selections if they become incompatible due to included components
        if (isCpuIncluded) setSelectedCpuId('');
        if (isRamIncluded) setSelectedRamId('');
        if (isDiskIncluded) setSelectedDiskIds([]);
  
        // Also handle the case where the number of *addable* disks changes
        if (!isDiskIncluded && selectedDiskIds.length > maxDisksToAdd) {
           setSelectedDiskIds(prev => prev.slice(0, maxDisksToAdd));
        }
  
      } else {
        // Reset everything if no chassis selected
        setSelectedCpuId('');
        setSelectedRamId('');
        setSelectedDiskIds([]);
      }
    }, [selectedChassis, isCpuIncluded, isRamIncluded, isDiskIncluded, maxDisksToAdd]); // Add inclusion flags to dependencies
  
  
    // --- Event Handlers ---
    const handleChassisChange = (event) => {
      setSelectedChassisId(event.target.value);
      // State resets will happen in the useEffect above
    };
  
    const handleCpuChange = (event) => {
      // Should not be callable if CPU is included due to disabled attribute, but double-check
      if (!isCpuIncluded) {
        setSelectedCpuId(event.target.value);
      }
    };
  
    const handleRamChange = (event) => {
      if (!isRamIncluded) {
          setSelectedRamId(event.target.value);
       }
    };
  
    const handleAddDisk = (diskIdToAdd) => {
      // Only add if disks aren't included and we're below the limit
      if (!isDiskIncluded && diskIdToAdd && selectedDisks.length < maxDisksToAdd) {
        setSelectedDiskIds(prev => [...prev, diskIdToAdd]);
      }
    };
  
    const handleRemoveDisk = (indexToRemove) => {
       // Only allow removal if disks aren't included
       if (!isDiskIncluded) {
          setSelectedDiskIds(prev => prev.filter((_, index) => index !== indexToRemove));
       }
    };
  
    // --- Calculations ---
    const totalPrice = useMemo(() => {
      let total = 0;
      if (selectedChassis) {
          total += selectedChassis.price; // Chassis price always included
          // Only add prices for components *selected by the user* (i.e., not included)
          if (!isCpuIncluded && effectiveCpu) total += effectiveCpu.price;
          if (!isRamIncluded && selectedRam) total += selectedRam.price;
          if (!isDiskIncluded) {
              selectedDisks.forEach(disk => {
                  if (disk) total += disk.price;
              });
          }
      }
      return total;
    }, [selectedChassis, effectiveCpu, selectedRam, selectedDisks, isCpuIncluded, isRamIncluded, isDiskIncluded]);
  
    const totalDiskSizeGB = useMemo(() => {
        // If disk is included, use its size. Otherwise, sum selected disks.
        if (isDiskIncluded) {
            return selectedChassis?.disk_included || 0;
        } else {
            return selectedDisks.reduce((sum, disk) => sum + (disk?.size_gb || 0), 0);
        }
    }, [selectedDisks, selectedChassis, isDiskIncluded]);
  
    const ramSizeGB = useMemo(() => {
        // If RAM is included, use its size. Otherwise, use selected RAM size.
        return isRamIncluded ? selectedChassis?.ram_included : selectedRam?.size_gb;
    }, [selectedChassis, isRamIncluded, selectedRam]);
  
    const ramSpeed = useMemo(() => {
        // Speed only available if user selected RAM
        return isRamIncluded ? 'N/A' : selectedRam?.speed;
    }, [isRamIncluded, selectedRam])
  
    // --- Rendering ---
    return (
      <div className={styles.builderContainer}>
        <h1>Ludus Mini PC Builder</h1>
        <p>Select components to configure your Mini PC for hosting Ludus.</p>
  
        <div className={styles.configurator}>
          {/* Chassis Selection */}
          <div className={styles.componentSection}>
            <label htmlFor="chassis-select">Chassis:</label>
            <select id="chassis-select" value={selectedChassisId} onChange={handleChassisChange}>
              <option value="">-- Select Chassis --</option>
              {componentData.chassis.map(chassis => (
                <option key={chassis.id} value={chassis.id}>
                  {chassis.name} (${chassis.price})
                </option>
              ))}
            </select>
            {selectedChassis && <a href={selectedChassis.link} target="_blank" rel="noopener noreferrer" className={styles.componentLink}>View Details</a>}
          </div>
  
          {/* CPU Selection */}
          <div className={styles.componentSection}>
              <label htmlFor="cpu-select">CPU:</label>
              {isCpuIncluded ? (
                  // Display included CPU info
                  <p>
                      <strong>{includedCpuObject?.name || selectedChassis.cpu_included}</strong> (Included)
                      {includedCpuObject?.link && <a href={includedCpuObject.link} target="_blank" rel="noopener noreferrer" className={styles.componentLinkSmall}>Details</a>}
                   </p>
              ) : (
                   // Show dropdown for selection
                  <>
                      <select
                          id="cpu-select"
                          value={selectedCpuId}
                          onChange={handleCpuChange}
                          disabled={!selectedChassis || isCpuIncluded} // Disable if no chassis or CPU included
                      >
                          <option value="">-- Select CPU --</option>
                          {componentData.cpu
                              .filter(cpu => cpu.price > 0) // Don't show CPUs that are only included
                              .map(cpu => (
                              <option key={cpu.id} value={cpu.id}>
                                  {cpu.name} (${cpu.price})
                              </option>
                          ))}
                      </select>
                      {effectiveCpu && !isCpuIncluded && <a href={effectiveCpu.link} target="_blank" rel="noopener noreferrer" className={styles.componentLink}>View Details</a>}
                   </>
              )}
          </div>
  
  
          {/* RAM Selection */}
           <div className={styles.componentSection}>
            <label htmlFor="ram-select">RAM:</label>
             {isRamIncluded ? (
                  // Display included RAM info
                  <p><strong>{selectedChassis.ram_included}GB RAM</strong> (Included)</p>
              ) : (
                  // Show dropdown for selection
                  <>
                      <select
                          id="ram-select"
                          value={selectedRamId}
                          onChange={handleRamChange}
                          disabled={!selectedChassis || isRamIncluded} // Disable if no chassis or RAM included
                      >
                          <option value="">-- Select RAM --</option>
                          {componentData.ram.map(ram => (
                          <option key={ram.id} value={ram.id}>
                              {ram.name} (${ram.price})
                          </option>
                          ))}
                      </select>
                       {selectedRam && <a href={selectedRam.link} target="_blank" rel="noopener noreferrer" className={styles.componentLink}>View Details</a>}
                   </>
             )}
          </div>
  
          {/* Disk Selection */}
          <div className={styles.componentSection}>
              <label>Disks (Add up to: {maxDisksToAdd}):</label>
              {isDiskIncluded ? (
                   // Display included Disk info
                   <p><strong>{formatSizeTB(selectedChassis.disk_included)} Disk Storage</strong> (Included)</p>
              ) : (
                  // Show UI for adding/removing disks
                  <>
                      <div className={styles.diskSelector}>
                          <select
                              id="disk-add-select"
                              // Disable if no chassis, disk included, or max addable reached
                              disabled={!selectedChassis || isDiskIncluded || selectedDisks.length >= maxDisksToAdd}
                              onChange={(e) => { handleAddDisk(e.target.value); e.target.value = '';}}
                              value=""
                          >
                              <option value="" disabled>-- Add a Disk --</option>
                              {componentData.disk.map(disk => (
                                  <option key={disk.id} value={disk.id}>
                                      {disk.name} ({formatSizeTB(disk.size_gb)} {disk.type}) (${disk.price})
                                  </option>
                              ))}
                          </select>
                          {selectedDisks.length >= maxDisksToAdd && maxDisksToAdd > 0 && <span className={styles.diskLimitReached}> Max disks reached</span>}
                          {maxDisksToAdd === 0 && selectedChassis && <span className={styles.diskLimitReached}> Cannot add disks to this chassis</span>}
                      </div>
                      <ul className={styles.selectedDisksList}>
                          {selectedDisks.map((disk, index) => (
                              <li key={`${disk.id}-${index}`}>
                                  {disk.name} ({formatSizeTB(disk.size_gb)} {disk.type})
                                  <a href={disk.link} target="_blank" rel="noopener noreferrer" className={styles.componentLinkSmall}>Link</a>
                                  <button onClick={() => handleRemoveDisk(index)} className={styles.removeButton}>
                                      Remove
                                  </button>
                              </li>
                          ))}
                      </ul>
                  </>
               )}
          </div>
        </div>
  
        {/* Summary Section */}
        <div className={styles.summarySection}>
          <h2>Build Summary</h2>
          {selectedChassis ? (
            <>
              <p><strong>Chassis:</strong>{' '}
                  <a href={selectedChassis.link} target="_blank" rel="noopener noreferrer">
                      {selectedChassis.name}
                  </a>{' '}
                  (${selectedChassis.price})
              </p>
  
              <p><strong>CPU:</strong>{' '}
                  {isCpuIncluded ? (
                       <>{includedCpuObject?.name || selectedChassis.cpu_included} (Included)</>
                  ) : effectiveCpu ? (
                       <>
                          <a href={effectiveCpu.link} target="_blank" rel="noopener noreferrer">
                              {effectiveCpu.name}
                          </a>{' '}
                          (${effectiveCpu.price})
                      </>
                  ) : 'Not Selected'}
              </p>
  
               <p><strong>RAM:</strong>{' '}
                   {isRamIncluded ? (
                       <>{selectedChassis.ram_included}GB (Included)</>
                   ) : selectedRam ? (
                        <>
                           <a href={selectedRam.link} target="_blank" rel="noopener noreferrer">
                               {selectedRam.name}
                           </a>{' '}
                           (${selectedRam.price})
                       </>
                   ) : 'Not Selected'}
               </p>
  
              <div><strong>Disks:</strong>
                {isDiskIncluded ? (
                    <ul><li>{formatSizeTB(selectedChassis.disk_included)} (Included)</li></ul>
                ) : selectedDisks.length > 0 ? (
                  <ul>
                    {selectedDisks.map((disk, index) => (
                      <li key={`summary-${disk.id}-${index}`}>
                          <a href={disk.link} target="_blank" rel="noopener noreferrer">
                              {disk.name}
                          </a>{' '}
                          ({formatSizeTB(disk.size_gb)} {disk.type}) (${disk.price})
                      </li>
                    ))}
                    <li>
                      <a href="https://amzn.to/3WbxGhS" target="_blank" rel="noopener noreferrer">
                        Consider adding low profile copper heatsink(s)
                      </a>
                    </li>
                  </ul>
                  
                ) : ' No disks selected'}
              </div>
  
              <hr/>
  
              <h3>Stats</h3>
              <ul>
                {(effectiveCpu?.cores && effectiveCpu?.threads) && (
                    <li>
                        <strong>CPU Cores/Threads:</strong> {effectiveCpu.cores} / {effectiveCpu.threads}
                    </li>
                )}

                {effectiveCpu?.passmark && (
                    <li>
                        <strong>CPU Passmark Score:</strong> {effectiveCpu.passmark.toLocaleString()}
                    </li>
                )}

                {ramSizeGB && ramSizeGB > 0 && ( // Ensure size is positive
                    <li>
                        <strong>RAM Size:</strong> {ramSizeGB} GB
                    </li>
                )}

                {ramSpeed && ramSpeed !== 'N/A' && ( // ramSpeed is already 'N/A' or a value, check truthiness
                    <li>
                        <strong>RAM Speed:</strong> {ramSpeed} MT/s
                    </li>
                )}

                {totalDiskSizeGB > 0 && ( // Ensure size is positive
                    <li>
                        <strong>Total Disk Size:</strong> {formatSizeTB(totalDiskSizeGB)}
                    </li>
                )}
            </ul>
  
              <hr/>
  
              <h3 className={styles.totalPrice}>Total Estimated Price: ${totalPrice.toFixed(2)}</h3>
              <p className={styles.disclaimer}>Prices are estimates and may vary. Links go to external sites.</p>
  
            </>
          ) : (
            <p>Please select a chassis to begin configuring your Mini PC.</p>
          )}
        </div>
      </div>
    );
  }

export default MiniPcBuilder;